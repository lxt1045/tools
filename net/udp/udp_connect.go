package udp

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

// Connect udp接口启动的时候执行，inCh, outCh是输入、输出chan，为了和后端处理模块解耦
func Connect(ctx context.Context, wg *sync.WaitGroup, addr string) (
	recvCh, sendCh chan Pkg, err error) {

	go kcpUpdata(wg) //每一个kcp连接都需要定期巧用updata()以驱动kcp循环

	recvCh, sendCh = make(chan Pkg), make(chan Pkg)
	//addr = "127.0.0.1:8501"
	c, err := net.Dial("udp", addr)
	if err != nil {
		err = fmt.Errorf("Dial got error:%v", err)
		log.Println(err)
		return
	}
	connUDP := c.(*net.UDPConn)

	udpAddr, err := net.ResolveUDPAddr("udp", connUDP.RemoteAddr().String())
	if err != nil {
		log.Printf("error:%v", err)
		return
	}
	conn := addr2Conn(udpAddr, connUDP)
	log.Printf("udp listening on: %s", addr)

	wg.Add(1)
	go func(ctx context.Context) { //判断ctx是否被取消了，如果是就退出
		<-ctx.Done()
		connUDP.Close()
		wg.Done()
	}(ctx)

	go goSend(sendCh, conn, wg)  //发送数据
	go recv(recvCh, connUDP, wg) //接收数据
	wg.Add(2)
	return
}
func goSend(sendCh chan Pkg, conn *Conn, wg *sync.WaitGroup) {
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
