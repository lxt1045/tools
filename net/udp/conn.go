package udp

import (
	"container/list"
	"fmt"
	"log"
	"net"
	"sync"

	kcp "github.com/xtaci/kcp-go"
)

//connsMap 用于保存连接相关的上下文
var connsMap sync.Map //map[connID uint64]*Conn  //用于保存网络连接

//Conn 每条连接建立一个Conn
type Conn struct {
	addr   *net.UDPAddr //只在创建的时候写入，其他时候只读，所以不加锁读
	connID uint64
	conn   *net.UDPConn

	kcp         *kcp.KCP      //kcp线程不安全，所以必须加锁
	listE       *list.Element //kcp updata()的list,delete的时候要用到，在加入list的时候更新
	*sync.Mutex               //kcp操作锁,因为有可能并发创建kcp,所以必须在Conn创建之初创建锁
	refleshTime int64         //每次收到数据包，就更新这个时间，在kcp_updata()的时候检查这个时间，超时则删除
}

func consistentHash(addr *net.UDPAddr) uint64 {
	//binary.LittleEndian.PutUint16(ret[4:6], uint16(addr.Port))
	b := addr.IP.To4()

	//最好加上serviceID，以保证在所有Services中保持唯一！！！
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 |
		uint64(b[3])<<24 | uint64(uint16(addr.Port))<<32
}

//通过*net.UDPAddr查找或生成一个*Conn，保证返回可用结果
func addr2Conn(addr *net.UDPAddr, connUDP *net.UDPConn, conv uint32,isWriteTo bool) (conn *Conn) {
	connID := consistentHash(addr)
	I, ok := connsMap.Load(connID) //先用Load，而不是用LoadOrStore()，因为后者每次都生成一个数据结构成本有点高
	if ok {
		if p, ok := I.(*Conn); ok {
			return p
		}
	}
	addrTo:=addr
	if !isWriteTo{
		addrTo=nil
	}
	pkcp := kcp.NewKCP(conv, KcpOutput(connUDP, addrTo))
	pkcp.WndSize(128, 128) //设置最大收发窗口为128
	// 第1个参数 nodelay-启用以后若干常规加速将启动
	// 第2个参数 interval为内部处理时钟，默认设置为 10ms
	// 第3个参数 resend为快速重传指标，设置为2
	// 第4个参数 为是否禁用常规流控，这里禁止
	pkcp.NoDelay(0, 10, 0, 0) // 默认模式
	//conn.kcp.NoDelay(0, 10, 0, 1) // 普通模式，关闭流控等
	//conn.kcp.NoDelay(1, 10, 2, 1) // 启动快速模式
	p := &Conn{
		addr:   addr,
		connID: connID,
		conn:   connUDP,
		kcp:    pkcp,            //kcp.NewKCP(),//在第一次收到可靠连接的时候创建
		Mutex:  new(sync.Mutex), //kcp操作锁,因为有可能并发创建kcp,所以必须在Conn创建之初创建锁
	}
	I, loaded := connsMap.LoadOrStore(connID, p) //为了强一致性，用LoadOrStore()
	if loaded {
		if p, ok := I.(*Conn); ok {
			return p
		}
	}

	updataAddCh <- p //通知updata()协程增加kcp
	return p
}

//通过*net.UDPAddr查找*Conn，找不到则返回nil
func connID2Conn(connID uint64) (conn *Conn) {
	I, ok := connsMap.Load(connID)
	if !ok {
		return nil
	}

	conn, ok = I.(*Conn)
	if !ok {
		connsMap.Delete(connID)
		return nil
	}
	return
}

//通过*net.UDPAddr查找*Conn，找不到则返回nil
func connIDRemoveConn(connID uint64) {
	I, ok := connsMap.Load(connID)
	if !ok {
		return
	}
	conn, ok := I.(*Conn)
	if !ok {
		connsMap.Delete(connID)
		return
	}
	removeConn(conn)
	return
}
func removeConn(conn *Conn) {
	conn.Lock()
	updataDelCh <- conn.listE //通过更新list删除自己
	conn.listE = nil
	conn.kcp = nil //清空kcp
	connsMap.Delete(conn.connID)
	conn.Unlock()
	return
}
func addrRemoveConn(addr *net.UDPAddr) {
	connID := consistentHash(addr)
	connIDRemoveConn(connID)
}

func (c *Conn) kcpSend(bufSend []byte) (err error) {
	c.Lock()
	defer c.Unlock()
	if c.kcp != nil {
		if m := c.kcp.Send(bufSend); m < 0 {
			err = fmt.Errorf("kcp.Send error, bufSend:%v", bufSend)
			return
		}
	} else {
		err = fmt.Errorf("c.kcp == nil")
		return
	}
	return
}
func (c *Conn) SendTo(pkg Pkg) (err error) {
	bufSend, err := pkg.Encode()
	if err != nil {
		log.Println(err)
		return
	}

	//以下处理KCP消息
	if pkg.Guar {
		err = c.kcpSend(bufSend)
		if err != nil {
			log.Println(err)
			return
		}
	} else {
		n, e := c.conn.WriteToUDP(bufSend, c.addr)
		if err = e; err != nil || n != len(bufSend) {
			log.Panicf("error during read:%v,n:%d\n", err, n)
			return
		}
	}
	return
}

func (c *Conn) Send(pkg Pkg) (err error) {
	bufSend, err := pkg.Encode()
	if err != nil {
		log.Println(err)
		return
	}

	//以下处理KCP消息
	if pkg.Guar {
		err = c.kcpSend(bufSend)
		if err != nil {
			log.Println(err)
			return
		}
	} else {
		n, e := c.conn.Write(bufSend)
		if err = e; err != nil || n != len(bufSend) {
			log.Panicf("error during read:%v,n:%d\n", err, n)
			return
		}
	}
	return
}
