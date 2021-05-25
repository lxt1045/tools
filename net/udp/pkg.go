package udp

import (
	"fmt"
	"log"
)

// Pkg 是用于在IO层(消息收发层)和路由层(消息分发层)之间交互的消息
type Pkg struct {
	MsgType uint16
	Guar    bool   //是否保证到达，false表示不保证到达
	ConnID  uint64 //UserID uint64 // 用于获取UDPAddr;
	Addr    string
	Data    []byte
}

func (p Pkg) Encode() (bufSend []byte, err error) {
	msgLen := len(p.Data) + MSG_HEADER_LEN
	bufSend = make([]byte, msgLen)
	if msgLen > udpBufLen /*0xffff*/ { //消息体超大
		err = fmt.Errorf("send error, msg too large ,p:[%v]", p)
		log.Println(err)
		return
	}
	guar := Guar_NO
	if p.Guar {
		guar = Guar_YES
	}
	h := Header{
		Len:  uint16(msgLen - MSG_HEADER_LEN), // 消息体的长度
		Type: p.MsgType,                       // 消息类型
		ID:   1,                               // 消息ID
		Ver:  1,                               // 版本号
		Guar: guar,                            // 保证到达
	}
	buf, err := h.Serialize(bufSend)
	if err != nil {
		log.Println(err)
		return
	}
	copy(buf, p.Data) //data载体放在header之后
	return
}

func Decode(buf []byte, connID uint64) (pkg Pkg, err error) {
	var h Header
	_, err = h.Deserialize(buf)
	if err != nil {
		err = fmt.Errorf("Deserialize got error:%v, header:%v", err, h)
		log.Println(err)
		return
	}
	n := len(buf)
	pkg.ConnID = connID
	if int(h.Len) > n-MSG_HEADER_LEN {
		err = fmt.Errorf("error during read,h.Len > n-MSG_HEADER_LEN, h.Len:%v, n:%d", h.Len, n)
		log.Println(err)
		return
	}
	pkg.Data = buf[MSG_HEADER_LEN:n]
	return
}
