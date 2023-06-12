package protocol

import (
	"bytes"
	"encoding/binary"
	"math/rand"

	"github.com/lesismal/nbio/nbhttp/websocket"
)

const (
	maskBit = 1 << 7
)

func EncodeClientMessage(messageType websocket.MessageType, data []byte) []byte {
	var (
		buf        []byte
		byte1      byte
		maskLen    int
		headLen    int
		bodyLen    = len(data)
		sendOpcode = true
		fin        = true
		isClient   = true
		compress   = false
	)

	if isClient {
		byte1 |= maskBit
		maskLen = 4
	}

	if bodyLen < 126 {
		headLen = 2 + maskLen
		buf = make([]byte, len(data)+headLen)
		buf[0] = 0
		buf[1] = (byte1 | byte(bodyLen))
	} else if bodyLen <= 65535 {
		headLen = 4 + maskLen
		buf = make([]byte, len(data)+headLen)
		buf[0] = 0
		buf[1] = (byte1 | 126)
		binary.BigEndian.PutUint16(buf[2:4], uint16(bodyLen))
	} else {
		headLen = 10 + maskLen
		buf = make([]byte, len(data)+headLen)
		buf[0] = 0
		buf[1] = (byte1 | 127)
		binary.BigEndian.PutUint64(buf[2:10], uint64(bodyLen))
	}

	if isClient {
		u32 := rand.Uint32()
		maskKey := []byte{byte(u32), byte(u32 >> 8), byte(u32 >> 16), byte(u32 >> 24)}
		copy(buf[headLen-4:headLen], maskKey)
		for i := 0; i < len(data); i++ {
			buf[headLen+i] = (data[i] ^ maskKey[i%4])
		}
	} else {
		copy(buf[headLen:], data)
	}

	// opcode
	if sendOpcode {
		buf[0] = byte(messageType)
	} else {
		buf[0] = 0
	}

	if compress {
		buf[0] |= 0x40
	}

	// fin
	if fin {
		buf[0] |= byte(0x80)
	}

	return buf
}

func BatchBuffers(buf []byte, rate, maxLen int) ([]byte, int, int) {
	batch := maxLen / len(buf)
	for (batch > 1) && (rate%batch != 0) {
		batch--
	}
	return bytes.Repeat(buf, batch), batch, rate / batch
}
