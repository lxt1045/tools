package udp

import (
	"context"
	"log"
	"net"
	"runtime"
	"sync/atomic"
	"time"
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
	cpuNum      = runtime.GOMAXPROCS(0)
	startStatus int32 //用于保证只启动一次
)

// Listen udp接口启动的时候执行，inCh, outCh是输入、输出chan，为了和后端处理模块解耦
func Listen(ctx context.Context, addr string) (recvCh, sendCh chan Pkg, err error) {
	if atomic.AddInt32(&startStatus, 1) > 1 {
		atomic.AddInt32(&startStatus, -1)
		log.Println(" udp.Listen() Can only be called once")
		return
	}

	go kcpUpdata() //每一个kcp连接都需要定期巧用updata()以驱动kcp循环

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

	go func(ctx context.Context) { //判断ctx是否被取消了，如果是就退出
		<-ctx.Done()
		listener.Close()
	}(ctx)

	//多线收发数据，可以提高数据收发速度
	cpuNum = 1
	for i := 0; i < cpuNum; i++ {
		go sendTo(sendCh, listener)   //发送数据
		go recvFrom(recvCh, listener) //接收数据
	}
	return
}

func recvFrom(recvCh chan Pkg, connUDP *net.UDPConn) {
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

		pkgs, err := decodePKG(bufRecv[:n], nil, func(conv uint32) *Conn {
			return addr2Conn(addr, connUDP, conv, true)
		})
		if err != nil {
			log.Printf("error during read:%v", err)
			break
		}

		for _, pkg := range pkgs {
			recvCh <- pkg
		}
	}
}
func decodePKG(bufRecv []byte, conn *Conn, fToConn func(conv uint32) *Conn) (pkgs []Pkg, err error) {
	var guar uint8
	decode8u(bufRecv, &guar) //先确认是否是可靠传输的消息

	// fmt.Printf("\n\nget UDP msg,  guar == Guar_YES:%v\n", guar == Guar_YES)

	if guar == Guar_YES {
		if conn == nil {
			var conv uint32
			decode32u(bufRecv[1:], &conv) //获取数据包的conv
			conn = fToConn(conv)
			atomic.StoreInt64(&conn.refleshTime, time.Now().Unix()) //这个不需要线程安全，并发更新谁成功了都是可以接受的,但是检查的时候，需要最新的值
		}
		conn.Lock()
		//以下操作会将bufRecv的数据复制到kcp的底层缓存，所以bufRecv可以快速重用
		m := conn.kcp.Input(bufRecv[1:], true, false) //bufRecv[1:], true, false：数据，正常包，ack延时发送
		conn.Unlock()
		bufRecv := make([]byte, len(bufRecv))
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
			pkg, e := Decode(bufRecv[:m], conn.connID)
			if err = e; err != nil {
				log.Println(err)
				return
			}
			pkg.Guar = true
			pkgs = append(pkgs, pkg)
		}
	} else if guar == Guar_NO {
		if conn == nil {
			conn = fToConn(0)
		}
		pkg, e := Decode(bufRecv, conn.connID)
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

func sendTo(sendCh chan Pkg, c *net.UDPConn) {
	for {
		pkg := <-sendCh
		conn := connID2Conn(pkg.ConnID)

		conn.SendTo(pkg)
	}
	log.Println("udp.send exit")
}
