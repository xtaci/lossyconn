package lossyconn

type DelayedPacket struct {
}

type DelayedWriter struct {
	chDelayedPacket chan DelayedPacket
}

func NewDelayedWriter() *DelayedWriter {
	dw := new(DelayedWriter)
	dw.chDelayedPacket = make(chan DelayedPacket)
	go dw.sendLoop()
	return dw
}

func (dw *DelayedWriter) sendLoop() {
	for p := range dw.chDelayedPacket {
		_ = p
	}
}
