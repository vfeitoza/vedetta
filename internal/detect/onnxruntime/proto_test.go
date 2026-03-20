package onnxruntime

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestProtoReaderReadVarint(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint64
	}{
		{"zero", []byte{0x00}, 0},
		{"one", []byte{0x01}, 1},
		{"127", []byte{0x7F}, 127},
		{"128", []byte{0x80, 0x01}, 128},
		{"300", []byte{0xAC, 0x02}, 300},
		{"16384", []byte{0x80, 0x80, 0x01}, 16384},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newProtoReader(tt.data)
			got, err := r.readVarint()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestProtoReaderReadVarintEOF(t *testing.T) {
	r := newProtoReader([]byte{})
	_, err := r.readVarint()
	if err == nil {
		t.Fatal("expected error for empty varint")
	}
}

func TestProtoReaderReadVarintOverflow(t *testing.T) {
	// 10 bytes of 0x80 followed by 0x01 would overflow
	data := make([]byte, 11)
	for i := range 10 {
		data[i] = 0x80
	}
	data[10] = 0x01
	r := newProtoReader(data)
	_, err := r.readVarint()
	if err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestProtoReaderReadTag(t *testing.T) {
	// Field 1, wire type 0 (varint) → tag = (1 << 3) | 0 = 8
	r := newProtoReader([]byte{0x08})
	fieldNum, wireType, err := r.readTag()
	if err != nil {
		t.Fatal(err)
	}
	if fieldNum != 1 || wireType != 0 {
		t.Errorf("got field=%d wire=%d, want field=1 wire=0", fieldNum, wireType)
	}

	// Field 2, wire type 2 (length-delimited) → tag = (2 << 3) | 2 = 18
	r = newProtoReader([]byte{0x12})
	fieldNum, wireType, err = r.readTag()
	if err != nil {
		t.Fatal(err)
	}
	if fieldNum != 2 || wireType != 2 {
		t.Errorf("got field=%d wire=%d, want field=2 wire=2", fieldNum, wireType)
	}
}

func TestProtoReaderReadBytes(t *testing.T) {
	// length=3, data="abc"
	r := newProtoReader([]byte{3, 'a', 'b', 'c'})
	data, err := r.readBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abc" {
		t.Errorf("got %q, want %q", data, "abc")
	}
}

func TestProtoReaderReadBytesEOF(t *testing.T) {
	// Claims 10 bytes but only has 2
	r := newProtoReader([]byte{10, 'a', 'b'})
	_, err := r.readBytes()
	if err == nil {
		t.Fatal("expected error for truncated bytes")
	}
}

func TestProtoReaderReadFixed32(t *testing.T) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], 42)
	r := newProtoReader(buf[:])
	v, err := r.readFixed32()
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Errorf("got %d, want 42", v)
	}
}

func TestProtoReaderReadFixed64(t *testing.T) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], 123456789)
	r := newProtoReader(buf[:])
	v, err := r.readFixed64()
	if err != nil {
		t.Fatal(err)
	}
	if v != 123456789 {
		t.Errorf("got %d, want 123456789", v)
	}
}

func TestProtoReaderSkip(t *testing.T) {
	// varint
	r := newProtoReader([]byte{0x96, 0x01}) // 150
	err := r.skip(wireVarint)
	if err != nil {
		t.Fatal(err)
	}
	if r.remaining() != 0 {
		t.Errorf("should have consumed all bytes, remaining=%d", r.remaining())
	}

	// 32-bit
	r = newProtoReader([]byte{1, 2, 3, 4})
	err = r.skip(wire32Bit)
	if err != nil {
		t.Fatal(err)
	}
	if r.remaining() != 0 {
		t.Errorf("should have consumed all 4 bytes")
	}

	// 64-bit
	r = newProtoReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	err = r.skip(wire64Bit)
	if err != nil {
		t.Fatal(err)
	}
	if r.remaining() != 0 {
		t.Errorf("should have consumed all 8 bytes")
	}

	// bytes
	r = newProtoReader([]byte{3, 'a', 'b', 'c'})
	err = r.skip(wireBytes)
	if err != nil {
		t.Fatal(err)
	}
	if r.remaining() != 0 {
		t.Errorf("should have consumed length+data")
	}
}

func TestProtoReaderSkipUnknownWireType(t *testing.T) {
	r := newProtoReader([]byte{1, 2, 3, 4})
	err := r.skip(3) // wire type 3 = start group (deprecated, unsupported)
	if err == nil {
		t.Fatal("expected error for unknown wire type")
	}
}

func TestReadPackedFloat32(t *testing.T) {
	var buf [12]byte
	binary.LittleEndian.PutUint32(buf[0:], math.Float32bits(1.0))
	binary.LittleEndian.PutUint32(buf[4:], math.Float32bits(2.5))
	binary.LittleEndian.PutUint32(buf[8:], math.Float32bits(-3.14))

	result := readPackedFloat32(buf[:])
	if len(result) != 3 {
		t.Fatalf("got %d floats, want 3", len(result))
	}
	assertApprox(t, result[0], 1.0, "packed float[0]")
	assertApprox(t, result[1], 2.5, "packed float[1]")
	if !approxEqual(result[2], -3.14, 0.01) {
		t.Errorf("packed float[2]: got %f, want -3.14", result[2])
	}
}

func TestReadPackedFloat32Empty(t *testing.T) {
	result := readPackedFloat32(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestReadPackedInt64(t *testing.T) {
	// Encode [1, 128, 300] as varints
	data := []byte{
		0x01,       // 1
		0x80, 0x01, // 128
		0xAC, 0x02, // 300
	}
	result, err := readPackedInt64(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d ints, want 3", len(result))
	}
	want := []int64{1, 128, 300}
	for i, v := range want {
		if result[i] != v {
			t.Errorf("packed int[%d]: got %d, want %d", i, result[i], v)
		}
	}
}

func TestReadPackedInt64Empty(t *testing.T) {
	result, err := readPackedInt64(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestProtoReaderSequentialReads(t *testing.T) {
	// Simulate reading multiple fields in sequence
	var buf []byte
	// Field 1, varint, value=42
	buf = append(buf, 0x08) // tag: field 1, wire varint
	buf = append(buf, 42)   // value
	// Field 2, bytes, value="hi"
	buf = append(buf, 0x12) // tag: field 2, wire bytes
	buf = append(buf, 2)    // length
	buf = append(buf, 'h', 'i')

	r := newProtoReader(buf)

	// Read first field
	f, w, err := r.readTag()
	if err != nil {
		t.Fatal(err)
	}
	if f != 1 || w != wireVarint {
		t.Fatalf("field 1: got f=%d w=%d", f, w)
	}
	v, err := r.readVarint()
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Errorf("field 1 value: got %d, want 42", v)
	}

	// Read second field
	f, w, err = r.readTag()
	if err != nil {
		t.Fatal(err)
	}
	if f != 2 || w != wireBytes {
		t.Fatalf("field 2: got f=%d w=%d", f, w)
	}
	data, err := r.readBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hi" {
		t.Errorf("field 2 value: got %q, want %q", data, "hi")
	}

	if r.remaining() != 0 {
		t.Errorf("should have consumed all bytes")
	}
}
