package udp

import (
	"encoding/hex"
	"fmt"
	"testing"
	"unsafe"
)

// Serialize 在传入的切片上序列化本对象，返回未使用的切片
func (p *Header) Serialize1(data []byte) ([]byte, error) {
	if MSG_HEADER_LEN > len(data) {
		return data, fmt.Errorf("input data(%v) err", data)
	}

	copy(*((*[]byte)(unsafe.Pointer(p))), data[:MSG_HEADER_LEN])

	return data[MSG_HEADER_LEN:], nil
}
func (p *Header) Deserialize1(data []byte) ([]byte, error) {
	if MSG_HEADER_LEN > len(data) {
		return data, fmt.Errorf("input data(%v) err", data)
	}
	copy(data[:MSG_HEADER_LEN], *((*[]byte)(unsafe.Pointer(p))))
	return data[MSG_HEADER_LEN:], nil
}
func TestMsgHeader(t *testing.T) {
	msgbody := []byte("hello word!你好，世界！")
	id := uint16(333)
	tp := uint16(888)
	ver := uint8(5)
	bodyLen := len(msgbody)
	buflen := MSG_HEADER_LEN + bodyLen
	oldBuf := make([]byte, buflen*3)
	newBuf := make([]byte, buflen*3)
	//保守的编码方式
	if false {
		h := Header{
			Guar: 0,               // 预留字段
			Ver:  ver,             // 版本号
			Len:  uint16(bodyLen), // 消息体的长度
			Type: uint16(tp),      // 消息类型
			ID:   id,              // 消息ID
		}
		bufSend := make([]byte, buflen)
		buf, _ := h.Serialize1(bufSend)
		copy(buf, msgbody)
		hex.Encode(oldBuf, bufSend)

		t.Logf("new:%v", h)
	}
	//激进的的编码方式
	{
		h := Header{
			Guar: 0,               // 预留字段
			Ver:  ver,             // 版本号
			Len:  uint16(bodyLen), // 消息体的长度
			Type: uint16(tp),      // 消息类型
			ID:   id,              // 消息ID
		}
		bufSend := make([]byte, buflen)
		buf, _ := h.Serialize(bufSend)
		copy(buf, msgbody)
		hex.Encode(newBuf, bufSend)

		t.Logf("new:%v", h)
	}
	if string(oldBuf) != string(newBuf) {
		//t.Errorf("oldBuf:%s, newBuf:%s", oldBuf, newBuf)
	}
	//test 解码
	{
		buf := make([]byte, len(newBuf))
		n, _ := hex.Decode(buf, newBuf)
		var h Header
		_, _ = h.Deserialize(buf[:n])

		if h.Len != uint16(bodyLen) ||
			h.Type != uint16(tp) ||
			h.ID != id ||
			h.Ver != ver ||
			h.Guar != 0 {
			t.Errorf("Deserialize error, h:%v", h)
		}
	}
}
