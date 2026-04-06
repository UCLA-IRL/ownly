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
	//MLS messages
	//+field:struct:MlsBlobRef
	MlsKeyPackage *MlsBlobRef `tlv:"0xD0"`
	//+field:struct:MlsBlobRef
	MlsCommit *MlsBlobRef `tlv:"0xD2"`
	//+field:struct:MlsBlobRef
	MlsWelcome *MlsBlobRef `tlv:"0xD4"`
}

type AeadBlock struct {
	//+field:string
	SessionID string `tlv:"0xC7"`
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

type MlsBlobRef struct {
	//+field:string
	Invitee string `tlv:"0x5A2"`
	//+field:string
	BlobName string `tlv:"0x5A4"`
	//+field:string
	SessionId string `tlv:"0x5A6"`
}
