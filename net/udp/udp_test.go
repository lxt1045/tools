package udp_test

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/lxt1045/tools/net/udp"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

func TestListen(t *testing.T) {
	addr := "127.0.0.1:18001"

	ctx, cancel := context.WithCancel(context.Background())
	recvCh, sendCh, err := udp.Listen(ctx, addr)
	if err != nil {
		t.Error(err)
		return
	}

	pkgR := <-recvCh
	for i := range pkgR.Data {
		if pkgR.Data[i] != byte(i) {
			//log.Panicf("pkg:%+v,data[%d]:%d,%d", pkgR, i, pkgR.Data[i], byte(i))
		}
	}
	log.Printf("pkgR:%+v,data:%s", pkgR, string(pkgR.Data))

	time.Sleep(1 * time.Millisecond)

	pkg := udp.Pkg{
		MsgType: 111,
		Guar:    true,
		Data:    make([]byte, 100*1024), //[]byte("hello word!"),
		ConnID:  pkgR.ConnID,
		//Addr:    to,
	}
	for i := range pkg.Data {
		pkg.Data[i] = byte(i)
	}
	pkg.Data = []byte("hello,vision")
	log.Println("1111")
	sendCh <- pkg

	log.Println("yyyy")
	time.Sleep(1 * time.Millisecond)

	time.Sleep(1 * time.Second)
	//select {}
	//cancel()
	_ = cancel

	t.Log("xxx")
}

func TestClient2(t *testing.T) {
	addr := "127.0.0.1:18001"

	ctx, cancel := context.WithCancel(context.Background())
	recvCh, sendCh, err := udp.Connect(ctx, addr)
	if err != nil {
		t.Error(err)
		return
	}

	pkg := udp.Pkg{
		MsgType: 111,
		Guar:    true,
		Data:    make([]byte, 100*1024), //[]byte("hello word!"),
		ConnID:  0,
		//Addr:    to,
	}
	for i := range pkg.Data {
		pkg.Data[i] = byte(i)
	}
	pkg.Data = []byte("hello word!")
	log.Println("1111")
	sendCh <- pkg

	pkgR := <-recvCh
	for i := range pkgR.Data {
		if pkgR.Data[i] != byte(i) {
			//log.Panicf("pkg:%+v,data[%d]:%d,%d", pkgR, i, pkgR.Data[i], byte(i))
		}
	}
	log.Printf("pkgR:%+v,data:%s", pkgR, string(pkgR.Data))

	time.Sleep(1 * time.Millisecond)

	log.Println("yyyy")
	time.Sleep(1 * time.Millisecond)

	time.Sleep(1 * time.Second)
	//select {}
	//cancel()
	_ = cancel

	t.Log("xxx")
}
