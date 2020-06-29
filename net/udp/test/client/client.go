package main

import (
	"container/list"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	"services/D_Go_V2.0_kit/io/udp"
	"services/D_Go_V2.0_kit/log"
	"services/D_Go_V2.0_sence/msg"
	//_ "services/D_Go_V2.0_sence/util"
	//log "github.com/cihub/seelog"
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
	runtime.GOMAXPROCS(8)
	goN = runtime.GOMAXPROCS(0)
	for i := 0; i < goN; i++ {
		sendConnList = append(sendConnList, list.New())
	}
}

type Conn struct {
	net.Conn
	ID       int
	pkgSendN int32
	pkgRecvN int32
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

	for _, l := range sendConnList {
		wg.Add(1)
		go send(&exitFlags, &sendPkgN, &wg, l) //在单协程中发送
	}

	for i := 0; i < goroutineMax; i++ {
		//conn, err := net.Dial("udp", "192.168.31.90:18080")
		//conn, err := net.Dial("udp", "127.0.0.1:18080")
		conn, err := net.Dial("udp", "192.168.31.242:18080")
		if err != nil {
			log.Infof("out, get error:%v", err)
			continue
		}
		wg.Add(1)
		pConn := &Conn{
			Conn: conn,
			ID:   i,
		}
		go recv(&exitFlags, i, &recvPkgN, &wg, pConn) //recv
		sendConnList[i%len(sendConnList)].PushBack(pConn)

		if i%1000 == 0 {
			log.Infof("start:%d", i)
		}
	}

	go statistics(&sendPkgN, &recvPkgN)
	time.Sleep(time.Second)
	wg.Wait()
	//wg.Wait()
}
func recv(exitFlags *int32, goroutineidex int, recvPkgN *int64, wg *sync.WaitGroup, conn *Conn) {
	var readBuf [1472]byte
	for {
		if atomic.LoadInt32(exitFlags) < 0 {
			break
		}

		n, err := conn.Read(readBuf[0:])
		if err != nil || n == 0 {
			//log.Errorf("read error:%v, n:%d,goroutineidex:%d", err, n, goroutineidex)
			break
		}
		now := time.Now().UnixNano()

		var h udp.Header
		_, err = h.Deserialize(readBuf[:n])
		var msgO msg.LoginReq
		err = proto.Unmarshal(readBuf[udp.MSG_HEADER_LEN:n], &msgO)
		if err != nil {
			atomic.AddInt32(&errorN, 1)
			continue
		}
		deta := now - int64(msgO.Stamp)
		if deta < 0 {
			atomic.AddInt32(&errorN, 1)
			continue
		}
		if deta > int64(time.Millisecond*100) {
			atomic.AddInt32(&pkgRecvDelay100msN, 1)
		}
		if deta > int64(time.Second) {
			atomic.AddInt32(&pkgRecvDelay1sN, 1)
		}
		atomic.AddInt64(&timeRecvDeleyT, deta)
		atomic.AddInt32(&conn.pkgRecvN, 1)
		atomic.AddInt64(recvPkgN, 1)
		//log.Debug(string(msg[:n]))
	}
	//log.Info("exit inside!")
	wg.Done()
}
func send(exitFlags *int32, pCount *int64, wg *sync.WaitGroup, l *list.List) {
	bufSend := make([]byte, 1024)
	msgLen, n := 0, 0
	var buf []byte
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
			msgO := msg.LoginReq{
				UserId:   uint64(conn.ID),
				AppId:    []byte("AppId"),
				DeviceId: []byte("DeviceId"),
				UseLast:  false,
				Token:    []byte("Token"),
				Sign:     []byte("Sign"),
				Name:     []byte(conn.LocalAddr().String()),
				Stamp:    time.Now().UnixNano(),
				Imid:     []byte("Imid"),
				Version:  []byte("Version"),
			}
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
				Guar: udp.Guar_NO,                         //01010101不可靠传输,10101010可靠传输
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
			n, err = conn.Write(bufSend[:msgLen])
			if err != nil || n != msgLen {
				//log.Errorf("error during read:%v,n:%d != msgLen:%d\n", err, n, msgLen)
				continue
			}
			//log.Debugf("send a msg:%s, conn:%s, addr:%s", string(bufSend[:msgLen]), conn.LocalAddr().String(), conn.RemoteAddr().String())
			atomic.AddInt32(&conn.pkgSendN, 1)
			atomic.AddInt64(pCount, 1)
			//goroutineMax
			//<-time.After(time.Duration(_delay))
		}
		//atomic.AddInt64(pCount, uint64(l.Len()))
		//log.Debugf("send msg:%s", string(sendBuf))

		//以下用于统计信息

		//time.Sleep(time.Millisecond * 100)
		//_delay := float64(time.Millisecond) * 20.0
		_delay := float64(time.Millisecond) * 2000.0
		time.Sleep(time.Duration(_delay))
	}
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
