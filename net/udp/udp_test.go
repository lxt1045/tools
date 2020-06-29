package udp_test

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/lxt1045/tools/net/udp"
)

func init() {
	log.SetFlags(log.Flags() | log.Lmicroseconds | log.Lshortfile) //log.Llongfile
}

func TestStart(t *testing.T) {
	addr := "127.0.0.1:18000"

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	recvCh, sendCh, err := udp.StartListen(ctx, &wg, addr)
	if err != nil {
		t.Error(err)
	}

	pkgR := <-recvCh
	for i := range pkgR.Data {
		if pkgR.Data[i] != byte(i) {
			log.Panicf("pkg:%+v,data[%d]:%d,%d", pkgR, i, pkgR.Data[i], byte(i))
		}
	}
	//log.Printf("pkgR:%+v,data:%s", pkgR, string(pkgR.Data))

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
