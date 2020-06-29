package udp

// Pkg 是用于在IO层(消息收发层)和路由层(消息分发层)之间交互的消息
type Pkg struct {
	MsgType uint16
	Guar    bool   //是否保证到达，false表示不保证到达
	ConnID  uint64 //UserID uint64 // 用于获取UDPAddr;
	Addr    string
	Data    []byte
}
