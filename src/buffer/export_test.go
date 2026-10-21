package buffer

import "github.com/OverlayFox/VRC-Haven/src/types"

var NewBufferStream = newBufferStream

func KeyFrameCount(b types.SubBuffer) int {
	sb := b.(*subBuffer) //nolint:forcetypeassert // only sub buffers from NewBufferStream are passed
	sb.mtx.RLock()
	defer sb.mtx.RUnlock()
	return sb.keyFrames.Len()
}
