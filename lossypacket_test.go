package lossyconn

import (
	"testing"
)

func TestLossyPacket(t *testing.T) {
	left, err := NewLossyConn(0.3, 200)
	if err != nil {
		t.Fatal(err)
	}

	right, err := NewLossyConn(0.2, 180)
	if err != nil {
		t.Fatal(err)
	}

	p := make([]byte, 1024)
	left.WriteTo(p, right.LocalAddr())
	right.ReadFrom(p)

	left.Close()
	right.Close()
	t.Logf("left:%v\n", left)
	t.Logf("right:%v\n", right)
}

func BenchmarkLossyPacket(b *testing.B) {
	left, err := NewLossyConn(0.3, 200)
	if err != nil {
		b.Fatal(err)
	}

	right, err := NewLossyConn(0.2, 180)
	if err != nil {
		b.Fatal(err)
	}

	p := make([]byte, 1024)

	for i := 0; i < b.N; i++ {
		left.WriteTo(p, right.LocalAddr())
	}
	right.Close()
	left.Close()

	for {
		if _, _, err := right.ReadFrom(p); err != nil {
			break
		}
	}
	b.Logf("left:%v\n", left)
	b.Logf("right:%v\n", right)
}
