package main

import (
	"container/list"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	kcp "github.com/xtaci/kcp-go"
	"services/D_Go_V2.0_kit/io/udp"
	"services/D_Go_V2.0_kit/log"
	"services/D_Go_V2.0_sence/msg"
)

var (
	sendConnList []*list.List
	goN          int

	timeRecvDeleyT     int64
	pkgRecvDelay100msN int32
	pkgRecvDelay1sN    int32
	errorN             int32
	goroutineMax       int

	lenOnePkg int
)

func init() {
	goroutineMax = 1
	runtime.GOMAXPROCS(4)
	goN = runtime.GOMAXPROCS(0)
	//for i := 0; i < goN; i++ {
	for i := 0; i < 1; i++ {
		sendConnList = append(sendConnList, list.New())
	}
}

type Conn struct {
	net.Conn
	ID       int
	pkgSendN int32
	pkgRecvN int32

	kcp         *kcp.KCP //kcp线程不安全，所以必须加锁
	*sync.Mutex          //update()的时候需要加锁，因为线程不安全

	sendIdx int32 //验证顺序接收
}
type Data struct {
	Socket string
	Time   int64
	ID     int64
}

func main() {
	var wg sync.WaitGroup
	exitFlags := int32(0)
	sendPkgN := int64(0)
	recvPkgN := int64(0)
	conv := uint32(0)

	for i := 1; i <= goroutineMax; i++ {
		//conn, err := net.Dial("udp", "192.168.31.90:18080")
		//conn, err := net.Dial("udp", "192.168.31.242:18080")
		//conn, err := net.Dial("udp", "127.0.0.1:18080")
		conn, err := net.Dial("udp", "127.0.0.1:8501")
		if err != nil {
			log.Infof("out, get error:%v", err)
			continue
		}
		wg.Add(1)
		pConn := &Conn{
			Conn:  conn,
			ID:    i,
			kcp:   kcp.NewKCP(atomic.AddUint32(&conv, 1), KcpOutput(conn)),
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

		pConn.kcp.Update() //要调用一次？？？

		go recv(&exitFlags, i, &recvPkgN, &wg, pConn) //recv
		sendConnList[i%len(sendConnList)].PushBack(pConn)

		if i%1000 == 0 {
			log.Infof("start:%d", i)
		}
	}

	for _, l := range sendConnList {
		wg.Add(1)
		go send(&exitFlags, &sendPkgN, &wg, l) //在单协程中发送
	}

	go statistics(&sendPkgN, &recvPkgN)

	time.Sleep(time.Second)
	wg.Wait()
	//wg.Wait()
}
func KcpOutput(conn net.Conn) (f func([]byte, int)) {
	f = func(buf []byte, size int) {
		//发送数据成熟时的异步回调函数，成熟数据为：buf[:size]，在这里把数据通过UDP发送出去
		//func copy(dst, src []Type) int //The source and destination may overlap.
		var bufSend []byte
		if len(buf) > size {
			bufSend = buf
		} else {
			bufSend = make([]byte, size+1)
		}
		copy(bufSend[1:], buf[:size])   //copy的src和dst可以重叠，所以直接使用
		bufSend[0] = byte(udp.Guar_YES) //加个header信息
		n, err := conn.Write(bufSend[:size+1])
		if err != nil || n != size+1 {
			log.Criticalf("error during kcp send:%v,n:%d\n", err, n)
		}
	}
	return
}
func recv(exitFlags *int32, goroutineidex int, recvPkgN *int64, wg *sync.WaitGroup, conn *Conn) {
	bufRecv := make([]byte, 1472)
	var msgO msg.LoginReq
	var nn uint64
	for {
		if atomic.LoadInt32(exitFlags) < 0 {
			break
		}

		n, err := conn.Read(bufRecv[0:])
		if err != nil || n == 0 {
			//log.Errorf("read error:%v, n:%d,goroutineidex:%d", err, n, goroutineidex)
			break
		}
		now := time.Now().UnixNano()
		if bufRecv[0] != byte(udp.Guar_YES) {
			log.Errorf("read error, Guar:%x", bufRecv[0])
			continue
		}

		//以下操作会将bufRecv的数据复制到kcp的底层缓存，所以bufRecv可以快速重用
		conn.Lock()
		m := conn.kcp.Input(bufRecv[1:n], true, false) //bufRecv[1:], true, false：数据，正常包，ack延时发送
		conn.Unlock()
		for m >= 0 {
			m = conn.kcp.Recv(bufRecv)
			if m <= 0 {
				if m == -3 {
					bufRecv = make([]byte, len(bufRecv)*2)
					m = 0
					continue
				}
				break
			}
			var h udp.Header
			_, err = h.Deserialize(bufRecv[:n])
			err = proto.Unmarshal(bufRecv[udp.MSG_HEADER_LEN:m], &msgO)
			if err != nil {
				atomic.AddInt32(&errorN, 1)
				continue
			}
			atomic.AddInt32(&conn.pkgRecvN, 1)
			atomic.AddInt64(recvPkgN, 1)
			if nn == 0 {
				nn = msgO.UserId
			} else {
				if nn+1 != msgO.UserId { //验证顺序接收
					atomic.AddInt32(&errorN, 1)
					fmt.Println("--:", msgO.UserId)
				}
				nn = msgO.UserId
			}
			delta := now - int64(msgO.Stamp)
			if delta < 0 {
				atomic.AddInt32(&errorN, 1)
				continue
			}
			if delta > int64(time.Millisecond*100) {
				if delta > int64(time.Second) {
					atomic.AddInt32(&pkgRecvDelay1sN, 1)
				} else {
					atomic.AddInt32(&pkgRecvDelay100msN, 1)
				}
			}
			atomic.AddInt64(&timeRecvDeleyT, delta)
		}

		//

		//
		//log.Debug(string(msg[:n]))
	}
	atomic.AddInt32(exitFlags, -2)
	//log.Info("exit inside!")
	wg.Done()
}
func send(exitFlags *int32, pCount *int64, wg *sync.WaitGroup, l *list.List) {
	bufSend := make([]byte, 1024)
	msgLen := 0
	var buf []byte
	msgO := msg.LoginReq{
		//UserId:   uint64(conn.ID),
		AppId:    []byte("AppId"),
		DeviceId: []byte("DeviceId"),
		UseLast:  false,
		Token:    []byte("Token"),
		Sign:     []byte("Sign"),
		//Name:     []byte(conn.LocalAddr().String()),
		//Stamp:   time.Now().UnixNano(),
		Imid:    []byte("Imid"),
		Version: []byte("Version"),
	}
	for {
		if atomic.LoadInt32(exitFlags) < 0 {
			break
		}
		for e := l.Front(); e != nil; e = e.Next() {
			if e.Value == nil {
				continue
			}
			conn, ok := e.Value.(*Conn)
			if !ok {
				continue
			}
			msgO.UserId = uint64(atomic.AddInt32(&conn.sendIdx, 1)) //uint64(conn.ID)
			msgO.Name = []byte(conn.LocalAddr().String())
			msgO.Stamp = time.Now().UnixNano()
			bytes, err := proto.Marshal(&msgO)
			if err != nil {
				log.Errorf("proto.Marshal Error:%v,pkgIO:[%v]", err, msgO)
				continue
			}
			msgLen = len(bytes) + udp.MSG_HEADER_LEN
			if len(bufSend) < msgLen {
				if msgLen > 0xffff { //消息体超大
					log.Errorf("send Error:%v,pkgIO:[%v]", "body is too big", msgO)
					continue
				}
				bufSend = make([]byte, msgLen)
			}
			h := udp.Header{
				Guar: udp.Guar_YES,                        //01010101不可靠传输,10101010可靠传输
				Ver:  1,                                   // 版本号
				Len:  uint16(msgLen - udp.MSG_HEADER_LEN), // 消息体的长度
				Type: uint16(msg.MSG_TYPE_LOGIN_REQ),      // 消息类型
				ID:   1,                                   // 消息ID
			}
			buf, err = h.Serialize(bufSend)
			copy(buf, bytes)
			if lenOnePkg == 0 {
				lenOnePkg = msgLen
			}
			if m := conn.kcp.Send(bufSend[:msgLen]); m < 0 {
				log.Errorf("kcp.Send error, pkg:%v", msgO)
			}
			atomic.AddInt32(&conn.pkgSendN, 1)
			atomic.AddInt64(pCount, 1)

			conn.Lock()
			checkT := conn.kcp.Check()
			nowMs := uint32(time.Now().UnixNano() / int64(time.Millisecond))
			if checkT <= nowMs {
				conn.kcp.Update()
			}
			conn.Unlock()
		}
		_delay := float64(time.Millisecond) * 10000 // 40.0
		time.Sleep(time.Duration(_delay))
	}
	atomic.AddInt32(exitFlags, -2)
	wg.Done()
}

func statistics(sendPkgN, recvPkgN *int64) {
	var lastSendN, lastRecvN, lastDelayT int64
	var lastTime int64

	for {
		recvN := atomic.LoadInt64(recvPkgN)
		sendN := atomic.LoadInt64(sendPkgN)
		delayT := atomic.LoadInt64(&timeRecvDeleyT)
		delay100msN := atomic.LoadInt32(&pkgRecvDelay100msN)
		delay1sN := atomic.LoadInt32(&pkgRecvDelay1sN)
		errN := atomic.LoadInt32(&errorN)

		now := time.Now().UnixNano()
		fps := float64(recvN-lastRecvN) / (float64(now-lastTime) / float64(time.Second))
		fpsSend := float64(sendN-lastSendN) / (float64(now-lastTime) / float64(time.Second))
		if lastTime == 0 {
			lastTime = now
			lastSendN = sendN
			lastRecvN = recvN
			lastDelayT = delayT
			<-time.After(time.Millisecond * 1000 * 3)
			continue
		}
		log.Infof("lost:%.3f, send fps:%.3fk, recv fps:%.3fk, recv:%dk, delay avg:%.3f,delay all avg:%.3f, >100ms:%.3f, >1s:%.3f, err:%d, lenOnePkg:%d",
			float64((sendN-lastSendN)-(recvN-lastRecvN))/float64(sendN-lastSendN), fpsSend/1000, fps/1000.0,
			recvN/1000, float64((delayT-lastDelayT)/int64(time.Millisecond))/float64(recvN-lastRecvN),
			float64(delayT/int64(time.Millisecond))/float64(recvN),
			float64(delay100msN)/float64(recvN), float64(delay1sN)/float64(recvN), errN, lenOnePkg)
		lastTime = now
		lastSendN = sendN
		lastRecvN = recvN
		lastDelayT = delayT
		for _, l := range sendConnList {
			_ = l
		}
		<-time.After(time.Millisecond * 1000 * 3)
	}
}
