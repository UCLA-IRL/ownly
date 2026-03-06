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
	//+field:struct:BootJoin
	BootJoin *BootJoin `tlv:"0xD0"`
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

type BootJoin struct {
	//+field:binary
	PreCertFullName []byte `tlv:"0x580"`
	//+field:binary
	AppPayload []byte `tlv:"0x582"`
}
