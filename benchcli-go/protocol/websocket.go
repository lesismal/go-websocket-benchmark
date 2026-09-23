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

// Pipeline is how many copies of a frame of frameLen bytes BenchRate merges
// into one write to a connection sent rate frames a second: pipeline of them
// when it is set, or else as many as maxLen bytes hold - one at least, even
// for a frame bigger than that. It is no more than rate, nor than limit, the
// messages a second the whole run may send when it is set, and it divides
// rate, so that every write is the same size. benchcli-uwscpp picks it the
// same way.
func Pipeline(frameLen, rate, maxLen, pipeline, limit int) int {
	batch := pipeline
	if batch <= 0 {
		batch = maxLen / frameLen
	}
	batch = max(1, min(batch, rate))
	if limit > 0 {
		batch = min(batch, limit)
	}
	for rate%batch != 0 {
		batch--
	}
	return batch
}

// BatchBuffers is the write BenchRate sends each tick: Pipeline copies of buf,
// and the ticks a second that make rate frames.
func BatchBuffers(buf []byte, rate, maxLen, pipeline, limit int) ([]byte, int, int) {
	batch := Pipeline(len(buf), rate, maxLen, pipeline, limit)
	return bytes.Repeat(buf, batch), batch, rate / batch
}
