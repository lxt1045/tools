package udp

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kcp "github.com/xtaci/kcp-go"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

func TestClient(t *testing.T) {
	conv := uint32(0)

	conn, err := net.Dial("udp", "127.0.0.1:18000")
	if err != nil {
		t.Fatalf("out, get error:%v", err)
	}
	pConn := &Conn{
		conn:  nil, //conn,
		kcp:   kcp.NewKCP(atomic.AddUint32(&conv, 1), KcpOutput1(conn)),
		Mutex: new(sync.Mutex),
	}
	pConn.kcp.WndSize(128, 128) //设置最大收发窗口为128
	// 第二个参数 nodelay-启用以后若干常规加速将启动
	// 第三个参数 interval为内部处理时钟，默认设置为 10ms
	// 第四个参数 resend为快速重传指标，设置为2
	// 第五个参数 为是否禁用常规流控，这里禁止
	pConn.kcp.NoDelay(0, 10, 0, 0) // 默认模式
	//conn.kcp.NoDelay(0, 10, 0, 1) // 普通模式，关闭流控等
	//pConn.kcp.NoDelay(1, 10, 2, 1) // 启动快速模式

	goUpdate(pConn)

	go send1(pConn) //在单协程中发送
	//go recv1(pConn)
	recv1(pConn)
	time.Sleep(1 * time.Second)
	//select {}
}
func KcpOutput1(conn net.Conn) (f func([]byte, int)) {
	f = func(buf []byte, size int) {
		//发送数据成熟时的异步回调函数，成熟数据为：buf[:size]，在这里把数据通过UDP发送出去
		//func copy(dst, src []Type) int //The source and destination may overlap.
		var bufSend []byte
		if len(buf) > size {
			bufSend = buf
		} else {
			bufSend = make([]byte, size+1)
		}
		copy(bufSend[1:], buf[:size]) //copy的src和dst可以重叠，所以直接使用
		encode8u(bufSend, Guar_YES)   //加个header信息
		n, err := conn.Write(bufSend[:size+1])
		if err != nil || n != size+1 {
			log.Panicf("error during kcp send:%v,n:%d\n", err, n)
		}
	}
	return
}
func recv1(conn *Conn) {
	bufRecv := make([]byte, 1472)
	var pkg Pkg
	for {
		n, err := conn.conn.Read(bufRecv[0:])
		if err != nil || n == 0 {
			log.Printf("read error:%v, n:%d", err, n)
			break
		}
		if bufRecv[0] != byte(Guar_YES) {
			log.Printf("read error, Guar:%x", bufRecv[0])
			continue
		}

		//以下操作会将bufRecv的数据复制到kcp的底层缓存，所以bufRecv可以快速重用
		conn.Lock()
		m := conn.kcp.Input(bufRecv[1:n], true, false) //bufRecv[1:], true, false：数据，正常包，ack延时发送
		conn.Unlock()
		for m >= 0 {
			//conn.Lock()
			m = conn.kcp.Recv(bufRecv)
			//conn.Unlock()
			if m <= 0 {
				if m == -3 {
					bufRecv = make([]byte, len(bufRecv)*2)
					m = 0
					continue
				}
				break
			}
			var h Header
			_, err = h.Deserialize(bufRecv[:n])
			if err != nil {
				log.Fatalln(err)
			}

			pkg.Data = append(bufRecv[:0:0], bufRecv[MSG_HEADER_LEN:m]...)

			for i := range pkg.Data {
				if pkg.Data[i] != byte(i) {
					log.Panicf("pkg:%+v,data[%d]:%d,%d", pkg, i, pkg.Data[i], byte(i))
				}
			}
			log.Printf("pkg:%+v,data:%s", pkg, pkg.Data)

			return
		}
	}
	time.Sleep(3 * time.Second)
	log.Println("exit inside!")
}
func send1(conn *Conn) {
	pkg := Pkg{
		MsgType: 111,
		Guar:    true,
		Data:    make([]byte, 10*1024), //[]byte("hello 世界！"),
	}
	for i := range pkg.Data {
		pkg.Data[i] = byte(i)
	}
	msgLen := len(pkg.Data) + MSG_HEADER_LEN
	bufSend := make([]byte, msgLen)
	h := Header{
		Guar: Guar_YES,                        //01010101不可靠传输,10101010可靠传输
		Ver:  1,                               // 版本号
		Len:  uint16(msgLen - MSG_HEADER_LEN), // 消息体的长度
		Type: 222,                             // 消息类型
	}
	buf, err := h.Serialize(bufSend)
	if err != nil {
		log.Fatalln(err)
	}
	copy(buf, pkg.Data)
	if m := conn.kcp.Send(bufSend[:msgLen]); m < 0 {
		log.Printf("kcp.Send error, pkg:%v", pkg)
	}
}
func goUpdate(conn *Conn) {
	conn.kcp.Update()
	go func() {
		for {
			<-time.After(time.Millisecond * time.Duration(10)) //10ms

			conn.Lock()
			checkT := conn.kcp.Check()
			nowMs := uint32(time.Now().UnixNano() / int64(time.Millisecond))
			if checkT <= nowMs {
				conn.kcp.Update()
			}
			conn.Unlock()
		}
	}()
}
