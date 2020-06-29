package udp

import (
	"container/list"
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
func addr2Conn(addr *net.UDPAddr, connUDP *net.UDPConn) (conn *Conn) {
	connID := consistentHash(addr)
	I, ok := connsMap.Load(connID) //先用Load，而不是用LoadOrStore()，因为后者每次都生成一个数据结构成本有点高
	if ok {
		if p, ok := I.(*Conn); ok {
			return p
		}
	}
	p := &Conn{
		addr:   addr,
		connID: connID,
		conn:   connUDP,
		kcp:    nil,             //kcp.NewKCP(),//在第一次收到可靠连接的时候创建
		Mutex:  new(sync.Mutex), //kcp操作锁,因为有可能并发创建kcp,所以必须在Conn创建之初创建锁
	}
	I, loaded := connsMap.LoadOrStore(connID, p) //为了强一致性，用LoadOrStore()
	if loaded {
		if p, ok := I.(*Conn); ok {
			return p
		}
	}
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

func (c *Conn) SendTo(pkg Pkg) (err error) {
	//以下处理KCP消息
	if pkg.Guar {
		c.Lock()
		if c.kcp != nil {
			if m := c.kcp.Send(bufSend[:msgLen]); m < 0 {
				log.Printf("kcp.Send error, pkg:%v", pkg)
			}
		} else {
			log.Printf("c.kcp == nil, pkg:%v", pkg)
		}
		c.Unlock()
	} else {
		connUDP := c
		if c.conn != nil {
			connUDP = c.conn
		}
		n, err = connUDP.WriteToUDP(bufSend[:msgLen], c.addr)
		if err != nil || n != msgLen {
			log.Panicf("error during read:%v,n:%d\n", err, n)
			break
		}
	}
	return
}
