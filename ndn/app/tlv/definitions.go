//go:generate gondn_tlv_gen
package tlv

type Message struct {
	//+field:struct:AeadBlock
	AeadBlock *AeadBlock `tlv:"0xC6"`
	//+field:struct:YjsDelta
	YjsDelta *YjsDelta `tlv:"0xC8"`
	//+field:struct:DSKRequest
	DSKRequest *DSKRequest `tlv:"0xCA"`
	//+field:struct:DSKResponse
	DSKResponse *DSKResponse `tlv:"0xCC"`
	//+field:struct:DSKACK
	DSKACK *DSKACK `tlv:"0xCE"`
	//+field:struct:RefreshPing
	RefreshPing *RefreshPing `tlv:"0xD0"`
	//+field:struct:RefreshAck
	RefreshAck *RefreshAck `tlv:"0xD2"`
	//+field:struct:RefreshRequest
	RefreshRequest *RefreshRequest `tlv:"0xD4"`
	//+field:struct:BootJoin
	BootJoin *BootJoin `tlv:"0xD6"`
}

type AeadBlock struct {
	//+field:binary
	IV []byte `tlv:"0xC8"`
	//+field:binary
	Ciphertext []byte `tlv:"0xCA"`
}

type YjsDelta struct {
	//+field:string
	UUID string `tlv:"0x478"`
	//+field:binary
	Binary []byte `tlv:"0x4B0"`
}

type DSKRequest struct {
	//+field:binary
	X25519Pub []byte `tlv:"0x578"`
	//+field:natural
	Expiry uint64 `tlv:"0x57A"`
}

type DSKResponse struct {
	//+field:binary
	X25519Peer []byte `tlv:"0x57A"`
	//+field:binary
	Ciphertext []byte `tlv:"0x57C"`
}

type DSKACK struct {
	//+field:binary
	X25519Peer []byte `tlv:"0x57A"`
}

type RefreshPing struct {
	//+field:string
	RequestId string `tlv:"0x5A0"`
	//+field:string
	Requester string `tlv:"0x5A2"`
	//+field:string
	SentAt string `tlv:"0x5A4"`
}

type RefreshAck struct {
	//+field:string
	RequestId string `tlv:"0x5A0"`
	//+field:string
	Requester string `tlv:"0x5A2"`
	//+field:string
	Responder string `tlv:"0x5A4"`
	//+field:natural
	Freshness uint64 `tlv:"0x5A6"`
	//+field:string
	SentAt string `tlv:"0x5A8"`
}

type RefreshRequest struct {
	//+field:string
	RequestId string `tlv:"0x5A0"`
	//+field:string
	Requester string `tlv:"0x5A2"`
	//+field:string
	Responder string `tlv:"0x5A4"`
	//+field:string
	SentAt string `tlv:"0x5A6"`
}

type BootJoin struct {
	//+field:binary
	PreCertFullName []byte `tlv:"0x580"`
	//+field:binary
	AppPayload []byte `tlv:"0x582"`
}
