package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Media files encrypt their first 128 KiB with an ISAAC64 keystream.

type randCtx64 struct {
	randCnt uint64
	seed    [256]uint64
	mm      [256]uint64
	aa      uint64
	bb      uint64
	cc      uint64
}

func newRandCtx64(key uint64) *randCtx64 {
	ctx := &randCtx64{randCnt: 255}
	ctx.init(key)
	return ctx
}

func (ctx *randCtx64) next() uint64 {
	result := ctx.seed[ctx.randCnt]
	if ctx.randCnt == 0 {
		ctx.isaac()
		ctx.randCnt = 255
	} else {
		ctx.randCnt--
	}
	return result
}

func (ctx *randCtx64) init(key uint64) {
	const golden = uint64(0x9e3779b97f4a7c13)
	a, b, c, d := golden, golden, golden, golden
	e, f, g, h := golden, golden, golden, golden

	ctx.seed[0] = key
	for i := 1; i < 256; i++ {
		ctx.seed[i] = 0
	}
	for i := 0; i < 4; i++ {
		mix(&a, &b, &c, &d, &e, &f, &g, &h)
	}
	for i := 0; i < 256; i += 8 {
		a += ctx.seed[i]
		b += ctx.seed[i+1]
		c += ctx.seed[i+2]
		d += ctx.seed[i+3]
		e += ctx.seed[i+4]
		f += ctx.seed[i+5]
		g += ctx.seed[i+6]
		h += ctx.seed[i+7]
		mix(&a, &b, &c, &d, &e, &f, &g, &h)
		ctx.mm[i], ctx.mm[i+1], ctx.mm[i+2], ctx.mm[i+3] = a, b, c, d
		ctx.mm[i+4], ctx.mm[i+5], ctx.mm[i+6], ctx.mm[i+7] = e, f, g, h
	}
	for i := 0; i < 256; i += 8 {
		a += ctx.mm[i]
		b += ctx.mm[i+1]
		c += ctx.mm[i+2]
		d += ctx.mm[i+3]
		e += ctx.mm[i+4]
		f += ctx.mm[i+5]
		g += ctx.mm[i+6]
		h += ctx.mm[i+7]
		mix(&a, &b, &c, &d, &e, &f, &g, &h)
		ctx.mm[i], ctx.mm[i+1], ctx.mm[i+2], ctx.mm[i+3] = a, b, c, d
		ctx.mm[i+4], ctx.mm[i+5], ctx.mm[i+6], ctx.mm[i+7] = e, f, g, h
	}
	ctx.isaac()
}

func (ctx *randCtx64) isaac() {
	ctx.cc++
	ctx.bb += ctx.cc
	for i := 0; i < 256; i++ {
		switch i % 4 {
		case 0:
			ctx.aa = ^(ctx.aa ^ (ctx.aa << 21))
		case 1:
			ctx.aa ^= ctx.aa >> 5
		case 2:
			ctx.aa ^= ctx.aa << 12
		case 3:
			ctx.aa ^= ctx.aa >> 33
		}
		ctx.aa += ctx.mm[(i+128)%256]
		x := ctx.mm[i]
		y := ctx.mm[(x>>3)%256] + ctx.aa + ctx.bb
		ctx.mm[i] = y
		ctx.bb = ctx.mm[(y>>11)%256] + x
		ctx.seed[i] = ctx.bb
	}
}

func mix(a, b, c, d, e, f, g, h *uint64) {
	*a -= *e
	*f ^= *h >> 9
	*h += *a
	*b -= *f
	*g ^= *a << 9
	*a += *b
	*c -= *g
	*h ^= *b >> 23
	*b += *c
	*d -= *h
	*a ^= *c << 15
	*c += *d
	*e -= *a
	*b ^= *d >> 14
	*d += *e
	*f -= *b
	*c ^= *e << 20
	*e += *f
	*g -= *c
	*d ^= *f >> 17
	*f += *g
	*h -= *d
	*e ^= *g << 14
	*g += *h
}

const encryptedRegion = 128 * 1024

func decryptBytes(data []byte, key uint64) {
	if len(data) == 0 || key == 0 {
		return
	}
	limit := len(data)
	if limit > encryptedRegion {
		limit = encryptedRegion
	}
	ctx := newRandCtx64(key)
	var block [8]byte
	for i := 0; i < limit; i += 8 {
		binary.BigEndian.PutUint64(block[:], ctx.next())
		for j := 0; j < 8 && i+j < limit; j++ {
			data[i+j] ^= block[j]
		}
	}
}

func decryptFileInPlace(path string, key uint64) error {
	if key == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, encryptedRegion)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return fmt.Errorf("read encrypted head: %w", err)
	}
	if n == 0 {
		return nil
	}
	decryptBytes(buf[:n], key)
	if _, err := f.WriteAt(buf[:n], 0); err != nil {
		return fmt.Errorf("write decrypted head: %w", err)
	}
	return nil
}
