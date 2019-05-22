package lossyconn

import (
	"log"
	"testing"
)

func TestLossyPacket(t *testing.T) {
	left, err := NewLossyPacketConn(0.3, 200)
	if err != nil {
		t.Fatal(err)
	}

	right, err := NewLossyPacketConn(0.2, 180)
	if err != nil {
		t.Fatal(err)
	}

	p := make([]byte, 1024)
	left.WriteTo(p, right.LocalAddr())
	right.ReadFrom(p)

	log.Printf("left:%v\n", left)
	log.Printf("right:%v\n", right)
}
