package main

// RIPEMD-160, needed for hash160 (the P2WPKH witness program) in the W-3
// holding-proof script derivation. Ported from golang.org/x/crypto/ripemd160
// (BSD-3-Clause, The Go Authors), reduced to the one-shot digest this package
// needs, so no new module dependency (and no vendor re-sync) is introduced for
// 100 lines of fixed arithmetic.

import (
	"crypto/sha256"
	"encoding/binary"
)

// hash160 is Bitcoin's HASH160: ripemd160(sha256(b)). A compressed public key
// hashed this way is the P2WPKH witness program.
func hash160(b []byte) []byte {
	sha := sha256.Sum256(b)
	return ripemd160Sum(sha[:])
}

var (
	rmdN  = [80]uint{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8, 3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12, 1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2, 4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13}
	rmdR  = [80]uint{11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8, 7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12, 11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5, 11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12, 9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6}
	rmdNP = [80]uint{5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12, 6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2, 15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13, 8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14, 12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11}
	rmdRP = [80]uint{8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6, 9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11, 9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5, 15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8, 8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11}
)

func ripemd160Block(s *[5]uint32, p []byte) {
	var x [16]uint32
	for i := 0; i < 16; i++ {
		x[i] = binary.LittleEndian.Uint32(p[4*i:])
	}
	a, b, c, d, e := s[0], s[1], s[2], s[3], s[4]
	aa, bb, cc, dd, ee := a, b, c, d, e
	var alpha, beta uint32

	// round 1
	i := 0
	for i < 16 {
		alpha = a + (b ^ c ^ d) + x[rmdN[i]]
		si := int(rmdR[i])
		alpha = (alpha<<si | alpha>>(32-si)) + e
		beta = c<<10 | c>>22
		a, b, c, d, e = e, alpha, b, beta, d

		alpha = aa + (bb ^ (cc | ^dd)) + x[rmdNP[i]] + 0x50a28be6
		si = int(rmdRP[i])
		alpha = (alpha<<si | alpha>>(32-si)) + ee
		beta = cc<<10 | cc>>22
		aa, bb, cc, dd, ee = ee, alpha, bb, beta, dd
		i++
	}
	// round 2
	for i < 32 {
		alpha = a + (b&c | ^b&d) + x[rmdN[i]] + 0x5a827999
		si := int(rmdR[i])
		alpha = (alpha<<si | alpha>>(32-si)) + e
		beta = c<<10 | c>>22
		a, b, c, d, e = e, alpha, b, beta, d

		alpha = aa + (bb&dd | cc&^dd) + x[rmdNP[i]] + 0x5c4dd124
		si = int(rmdRP[i])
		alpha = (alpha<<si | alpha>>(32-si)) + ee
		beta = cc<<10 | cc>>22
		aa, bb, cc, dd, ee = ee, alpha, bb, beta, dd
		i++
	}
	// round 3
	for i < 48 {
		alpha = a + (b | ^c ^ d) + x[rmdN[i]] + 0x6ed9eba1
		si := int(rmdR[i])
		alpha = (alpha<<si | alpha>>(32-si)) + e
		beta = c<<10 | c>>22
		a, b, c, d, e = e, alpha, b, beta, d

		alpha = aa + (bb | ^cc ^ dd) + x[rmdNP[i]] + 0x6d703ef3
		si = int(rmdRP[i])
		alpha = (alpha<<si | alpha>>(32-si)) + ee
		beta = cc<<10 | cc>>22
		aa, bb, cc, dd, ee = ee, alpha, bb, beta, dd
		i++
	}
	// round 4
	for i < 64 {
		alpha = a + (b&d | c&^d) + x[rmdN[i]] + 0x8f1bbcdc
		si := int(rmdR[i])
		alpha = (alpha<<si | alpha>>(32-si)) + e
		beta = c<<10 | c>>22
		a, b, c, d, e = e, alpha, b, beta, d

		alpha = aa + (bb&cc | ^bb&dd) + x[rmdNP[i]] + 0x7a6d76e9
		si = int(rmdRP[i])
		alpha = (alpha<<si | alpha>>(32-si)) + ee
		beta = cc<<10 | cc>>22
		aa, bb, cc, dd, ee = ee, alpha, bb, beta, dd
		i++
	}
	// round 5
	for i < 80 {
		alpha = a + (b ^ (c | ^d)) + x[rmdN[i]] + 0xa953fd4e
		si := int(rmdR[i])
		alpha = (alpha<<si | alpha>>(32-si)) + e
		beta = c<<10 | c>>22
		a, b, c, d, e = e, alpha, b, beta, d

		alpha = aa + (bb ^ cc ^ dd) + x[rmdNP[i]]
		si = int(rmdRP[i])
		alpha = (alpha<<si | alpha>>(32-si)) + ee
		beta = cc<<10 | cc>>22
		aa, bb, cc, dd, ee = ee, alpha, bb, beta, dd
		i++
	}
	// combine
	dd += c + s[1]
	s[1] = s[2] + d + ee
	s[2] = s[3] + e + aa
	s[3] = s[4] + a + bb
	s[4] = s[0] + b + cc
	s[0] = dd
}

// ripemd160Sum computes the RIPEMD-160 digest of b.
func ripemd160Sum(b []byte) []byte {
	s := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	length := uint64(len(b))
	for len(b) >= 64 {
		ripemd160Block(&s, b[:64])
		b = b[64:]
	}
	// padding
	var tail [128]byte
	n := copy(tail[:], b)
	tail[n] = 0x80
	padLen := 64
	if n >= 56 {
		padLen = 128
	}
	binary.LittleEndian.PutUint64(tail[padLen-8:], length<<3)
	for off := 0; off < padLen; off += 64 {
		ripemd160Block(&s, tail[off:off+64])
	}
	out := make([]byte, 20)
	for i, v := range s {
		binary.LittleEndian.PutUint32(out[4*i:], v)
	}
	return out
}
