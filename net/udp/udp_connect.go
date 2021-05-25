package udp

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

// Connect udp接口启动的时候执行，inCh, outCh是输入、输出chan，为了和后端处理模块解耦
func Connect(ctx context.Context, addr string) (recvCh, sendCh chan Pkg, err error) {

	go kcpUpdata() //每一个kcp连接都需要定期巧用updata()以驱动kcp循环

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
	conn := addr2Conn(udpAddr, connUDP, 0, false)
	log.Printf("udp listening on: %s,conn:%+v", addr, conn)

	go func(ctx context.Context) { //判断ctx是否被取消了，如果是就退出
		<-ctx.Done()
		connUDP.Close()
	}(ctx)

	go goSend(sendCh, conn) //发送数据
	go goRecv(recvCh, conn) //接收数据
	return
}
func goSend(sendCh chan Pkg, conn *Conn) {
	for {
		pkg := <-sendCh

		log.Printf("udp send,conn:%+v,data:%v", conn, pkg)
		conn.Send(pkg)

	}
	log.Println("udp.send exit")
}

func goRecv(recvCh chan Pkg, c *Conn) {
	bufRecv := make([]byte, udpBufLen)

	for {
		n, err := c.conn.Read(bufRecv)
		if err != nil || n == 0 {
			log.Printf("read error:%v, n:%d", err, n)
			break
		}
		atomic.StoreInt64(&c.refleshTime, time.Now().Unix()) //这个不需要线程安全，并发更新谁成功了都是可以接受的,但是检查的时候，需要最新的值

		pkgs, err := decodePKG(bufRecv[:n], c, nil)
		if err != nil {
			log.Printf("error during read:%v", err)
			break
		}

		for _, pkg := range pkgs {
			log.Printf("udp recv,conn:%+v,data:%v", c.conn, pkg)
			recvCh <- pkg
		}
	}
}
