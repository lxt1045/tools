package main

import (
	"context"
	//"encoding/json"
	"reflect"
	"runtime"
	"sync"
	"time"

	"os"
	"runtime/pprof"

	"services/D_Go_V2.0_kit/io/udp"
	"services/D_Go_V2.0_kit/log"
	"services/D_Go_V2.0_sence/conf"
	_ "services/D_Go_V2.0_sence/msg"
)

func main() {
	cpuf, err := os.Create("cpu_profile.prof")
	if err != nil {
		log.Error(err)
	}
	pprof.StartCPUProfile(cpuf)
	defer pprof.StopCPUProfile()

	runtime.GOMAXPROCS(4)
	conf.Conf.CPUNUM = runtime.GOMAXPROCS(0)
	conf.Conf.IOChanSize = 1024000
	//conf.Conf.Listen = ":18080"
	conf.Conf.Listen = ":8501"

	ctx, cancel := context.WithCancel(context.Background())
	logTracer := log.NewLogTrace(0, 0, 0) //
	recvCh, sendCh, err := udp.Listen(ctx, logTracer, &conf.Conf)
	if err != nil {
		log.Error(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for {
			//iface, closed := recvCh.Recv(32, 1)
			iface, closed := recvCh.Recv(1, 1)
			if closed {
				log.Infof("error during read, recvCh is closed, exit")
				break //chan 已关闭,则退出
			}
			if len(iface) == 0 {
				log.Infof("error during read, sendCh get len==0")
			}

			//
			pkg, ok := iface[0].(udp.Pkg)
			if !ok {
				log.Errorf("msg type Error, type:[%v],value:[%v]", reflect.TypeOf(iface[0]), iface[0])
				continue
			}
			y := pkg.Msg
			//log.Debugf("get msg:%s", y)
			//y.Receive()
			_ = y

			//js, err := json.MarshalIndent(&pkg, "", "\t")
			//			log.Infof("\n+++++++++++++++++++++++++++++++++++++++++\nerr:%v, msg.type:%v, typeID:%d, pkg:%v,\n msg:%v",
			//				err, reflect.TypeOf(pkg.Msg), pkg.Msg.MsgNO(), string(js), pkg.Msg)

			//
			pkgRet := udp.PkgRtn{
				Guar:   pkg.Guar,
				ConnID: pkg.ConnID,
				Msg:    pkg.Msg,

				LogT: pkg.LogT,
			}
			//			n, closed := sendCh.SendN(iface)
			//			if closed || n != len(iface) {
			full, closed := sendCh.Send(pkgRet)
			if closed || full {
				if closed {
					log.Infof("error during read, sendCh is closed, exit") //chan 已关闭
					break
				}
				//chan写满，可能需要做一些其它处理，比如：通知server已经忙不过来，请关闭稍后再试
				//log.Errorf("error during read, sendCh is full, pkg:%v", iface)
			}
		}
		wg.Done()
	}()
	//time.Sleep(time.Second * 60 * 1)
	//cancel()
	_ = time.Now()
	_ = cancel
	wg.Wait()
}
