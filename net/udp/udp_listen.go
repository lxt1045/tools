package udp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	kcp "github.com/xtaci/kcp-go"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

//IP协议规定路由器最少能转发：512数据+60IP首部最大+4预留=576字节,即最少可以转发512-8=504字节UDP数据
//内网一般1500字节,UDP：1500-IP(20)-UDP(8)=1472字节数据
const (
	udpBufLen        = 1 << 20 //1472    //UDP发送、接收，kcp发送、接收缓冲的大小
	ioChanLenDefault = 1024000 //当config没有设置时，使用此默认值
)

var (
	cpuNum              = runtime.GOMAXPROCS(0)
	startStatus         int32 //用于保证只启动一次
	recvN, lostN, sendN int64 //用于统计信息
)

// Listen udp接口启动的时候执行，inCh, outCh是输入、输出chan，为了和后端处理模块解耦
func Listen(ctx context.Context, wg *sync.WaitGroup, addr string) (
	recvCh, sendCh chan Pkg, err error) {
	if atomic.AddInt32(&startStatus, 1) > 1 {
		atomic.AddInt32(&startStatus, -1)
		log.Println(" udp.Listen() Can only be called once")
		return
	}

	go kcpUpdata(wg) //每一个kcp连接都需要定期巧用updata()以驱动kcp循环

	recvCh, sendCh = make(chan Pkg), make(chan Pkg)
	//addr = "127.0.0.1:8501"
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Printf("error:%v", err)
		return
	}
	listener, err := net.ListenUDP("udp", udpAddr)
	if err != nil || listener == nil {
		log.Printf("error:%v", err)
		return
	}
	log.Printf("udp listening on: %s", udpAddr)

	wg.Add(1)
	go func(ctx context.Context) { //判断ctx是否被取消了，如果是就退出
		<-ctx.Done()
		listener.Close()
		wg.Done()
	}(ctx)

	//多线收发数据，可以提高数据收发速度
	for i := 0; i < cpuNum; i++ {
		go sendTo(sendCh, listener, wg)   //发送数据
		go recvFrom(recvCh, listener, wg) //接收数据
		wg.Add(2)
	}
	return
}

func parseMsg(buf []byte, connID uint64) (pkg Pkg, err error) {
	var h Header
	_, err = h.Deserialize(buf)
	if err != nil {
		err = fmt.Errorf("Deserialize got error:%v, header:%v", err, h)
		log.Println(err)
		return
	}
	n := len(buf)
	pkg.ConnID = connID
	if int(h.Len) > n-MSG_HEADER_LEN {
		atomic.AddInt64(&lostN, 1)
		err = fmt.Errorf("error during read,h.Len > n-MSG_HEADER_LEN, h:%v, n:%d", h, n)
		return
	}
	pkg.Data = buf[MSG_HEADER_LEN:n]

	js, _ := json.MarshalIndent(&h, "", "\t")
	log.Printf("\n\n+++++++++++++++++++++++++\nheader:%v, \n\nbody:%x\n\n+++++++++++++++++++++++++\n\n",
		string(js), buf[MSG_HEADER_LEN:n])
	return
}
func recvFrom(recvCh chan Pkg, connUDP *net.UDPConn, wg *sync.WaitGroup) {
	var (
		addr    *net.UDPAddr
		err     error
		n       = 0
		bufRecv = make([]byte, udpBufLen)
	)
	for {
		n, addr, err = connUDP.ReadFromUDP(bufRecv)
		if err != nil || n <= 0 {
			log.Printf("error during read:%v, n:%d", err, n)
			break
		}
		atomic.AddInt64(&recvN, 1)
		conn := addr2Conn(addr, connUDP)
		atomic.StoreInt64(&conn.refleshTime, time.Now().Unix()) //这个不需要线程安全，并发更新谁成功了都是可以接受的,但是检查的时候，需要最新的值

		pkgs, err := decodePKG(bufRecv[:n], conn)
		if err != nil {
			log.Printf("error during read:%v", err)
			break
		}

		for _, pkg := range pkgs {
			recvCh <- pkg
		}
	}
	wg.Done()
}
func decodePKG(bufRecv []byte, conn *Conn) (pkgs []Pkg, err error) {
	var guar uint8
	decode8u(bufRecv, &guar) //先确认是否是可靠传输的消息

	fmt.Printf("\n\nget UDP msg,  guar == Guar_YES:%v\n", guar == Guar_YES)

	if guar == Guar_YES {
		conn.Lock()
		if conn.kcp == nil {
			var conv uint32
			decode32u(bufRecv[1:], &conv) //获取数据包的conv
			conn.kcp = kcp.NewKCP(conv, KcpOutput(conn.conn, conn.addr))
			conn.kcp.WndSize(128, 128) //设置最大收发窗口为128
			// 第1个参数 nodelay-启用以后若干常规加速将启动
			// 第2个参数 interval为内部处理时钟，默认设置为 10ms
			// 第3个参数 resend为快速重传指标，设置为2
			// 第4个参数 为是否禁用常规流控，这里禁止
			conn.kcp.NoDelay(0, 10, 0, 0) // 默认模式
			//conn.kcp.NoDelay(0, 10, 0, 1) // 普通模式，关闭流控等
			//conn.kcp.NoDelay(1, 10, 2, 1) // 启动快速模式
			updataAddCh <- conn //通知updata()协程增加kcp
		}
		//以下操作会将bufRecv的数据复制到kcp的底层缓存，所以bufRecv可以快速重用
		m := conn.kcp.Input(bufRecv[1:], true, false) //bufRecv[1:], true, false：数据，正常包，ack延时发送
		conn.Unlock()
		for m >= 0 {
			//这里要确认一下，kcp.Recv()是否要经过update()驱动，如果要驱动，则不能在这里处理
			conn.Lock()
			m = conn.kcp.Recv(bufRecv)
			conn.Unlock()
			if m <= 0 {
				if m == -3 {
					bufRecv = make([]byte, len(bufRecv)*2)
					m = 0
					continue
				}
				break
			}
			pkg, err := parseMsg(bufRecv[:m], conn.connID)
			if err != nil {
				log.Println(err)
				continue
			}
			pkg.Guar = true
			pkgs = append(pkgs, pkg)
		}
	} else if guar == Guar_NO {
		pkg, e := parseMsg(bufRecv, conn.connID)
		if err = e; err != nil {
			log.Println(err)
			return
		}
		pkgs = append(pkgs, pkg)
	} else {
		var h Header
		h.Deserialize(bufRecv)
		log.Printf("error during read, Header.Guar == %x, Header:%v", h.Guar, h)
	}
	return
}

func sendTo(sendCh chan Pkg, c *net.UDPConn, wg *sync.WaitGroup) {
	bufSend := make([]byte, udpBufLen)
	msgLen, n := 0, 0
	for {
		atomic.AddInt64(&sendN, 1)
		pkg := <-sendCh
		msgLen = len(pkg.Data) + MSG_HEADER_LEN
		if len(bufSend) < msgLen {
			if msgLen > udpBufLen /*0xffff*/ { //消息体超大
				log.Printf("send error, msg too large ,pkg:[%v]", pkg)
				continue
			}
			bufSend = make([]byte, msgLen)
		}
		guar := Guar_NO
		if pkg.Guar {
			guar = Guar_YES
		}
		h := Header{
			Len:  uint16(msgLen - MSG_HEADER_LEN), // 消息体的长度
			Type: pkg.MsgType,                     // 消息类型
			ID:   1,                               // 消息ID
			Ver:  1,                               // 版本号
			Guar: guar,                            // 保证到达
		}
		buf, err := h.Serialize(bufSend)
		if err != nil {
			log.Println(err)
			continue
		}
		copy(buf, pkg.Data) //data载体放在header之后

		conn := connID2Conn(pkg.ConnID)
		//以下处理KCP消息
		if pkg.Guar {
			conn.Lock()
			if conn.kcp != nil {
				if m := conn.kcp.Send(bufSend[:msgLen]); m < 0 {
					log.Printf("kcp.Send error, pkg:%v", pkg)
				}
			} else {
				log.Printf("conn.kcp == nil, pkg:%v", pkg)
			}
			conn.Unlock()
		} else {
			connUDP := c
			if conn.conn != nil {
				connUDP = conn.conn
			}
			n, err = connUDP.WriteToUDP(bufSend[:msgLen], conn.addr)
			if err != nil || n != msgLen {
				log.Panicf("error during read:%v,n:%d\n", err, n)
				break
			}
		}
	}
	log.Println("udp.send exit")
	wg.Done()
}
