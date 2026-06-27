//go:build darwin

package media

/*
#cgo LDFLAGS: -framework CoreMedia -framework CoreVideo -framework VideoToolbox
#include <VideoToolbox/VideoToolbox.h>
#include <CoreMedia/CoreMedia.h>
#include <stdlib.h>
#include <string.h>

// Callback context passed through the decompress call.
typedef struct {
    CVImageBufferRef outputImage;
    OSStatus         status;
} VTDecodeContext;

static void vtDecompressCallback(void *decompressionOutputRefCon,
                                  void *sourceFrameRefCon,
                                  OSStatus status,
                                  VTDecodeInfoFlags infoFlags,
                                  CVImageBufferRef imageBuffer,
                                  CMTime presentationTimeStamp,
                                  CMTime presentationDuration) {
    VTDecodeContext *ctx = (VTDecodeContext *)sourceFrameRefCon;
    ctx->status = status;
    if (status == noErr && imageBuffer != NULL) {
        CVPixelBufferRetain(imageBuffer);
        ctx->outputImage = imageBuffer;
    } else {
        ctx->outputImage = NULL;
    }
}

static VTDecompressionOutputCallbackRecord makeCallback() {
    VTDecompressionOutputCallbackRecord cb;
    cb.decompressionOutputCallback = vtDecompressCallback;
    cb.decompressionOutputRefCon = NULL;
    return cb;
}

// Create format description from SPS/PPS.
static OSStatus createFormatDescription(const uint8_t *sps, size_t spsLen,
                                         const uint8_t *pps, size_t ppsLen,
                                         CMVideoFormatDescriptionRef *outDesc) {
    const uint8_t *paramSets[2] = {sps, pps};
    size_t paramSetSizes[2] = {spsLen, ppsLen};
    return CMVideoFormatDescriptionCreateFromH264ParameterSets(
        kCFAllocatorDefault, 2, paramSets, paramSetSizes, 4, outDesc);
}

// Create decompression session.
static OSStatus createDecompSession(CMVideoFormatDescriptionRef formatDesc,
                                     VTDecompressionSessionRef *outSession) {
    CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    int pixFmt = kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
    CFNumberRef pixFmtNum = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &pixFmt);
    CFDictionarySetValue(attrs, kCVPixelBufferPixelFormatTypeKey, pixFmtNum);
    CFRelease(pixFmtNum);

    VTDecompressionOutputCallbackRecord cb = makeCallback();
    OSStatus status = VTDecompressionSessionCreate(
        kCFAllocatorDefault, formatDesc, NULL, attrs, &cb, outSession);
    CFRelease(attrs);
    return status;
}

// Decode a single sample buffer synchronously.
static OSStatus decodeSample(VTDecompressionSessionRef session,
                              CMVideoFormatDescriptionRef formatDesc,
                              const uint8_t *data, size_t dataLen,
                              VTDecodeContext *ctx) {
    CMBlockBufferRef blockBuf = NULL;
    OSStatus status = CMBlockBufferCreateWithMemoryBlock(
        kCFAllocatorDefault, (void *)data, dataLen, kCFAllocatorNull,
        NULL, 0, dataLen, 0, &blockBuf);
    if (status != noErr) return status;

    CMSampleBufferRef sampleBuf = NULL;
    const size_t sampleSize = dataLen;
    status = CMSampleBufferCreateReady(
        kCFAllocatorDefault, blockBuf, formatDesc,
        1, 0, NULL, 1, &sampleSize, &sampleBuf);
    CFRelease(blockBuf);
    if (status != noErr) return status;

    status = VTDecompressionSessionDecodeFrame(
        session, sampleBuf, 0, ctx, NULL);
    CFRelease(sampleBuf);
    if (status != noErr) return status;

    VTDecompressionSessionWaitForAsynchronousFrames(session);
    return ctx->status;
}

// Destroy session and format description.
static void destroySession(VTDecompressionSessionRef session, CMVideoFormatDescriptionRef fmt) {
    if (session) {
        VTDecompressionSessionInvalidate(session);
        CFRelease(session);
    }
    if (fmt) {
        CFRelease(fmt);
    }
}

// pixelBufferToPlanes locks the pixel buffer and copies Y and UV data into Go buffers.
// Returns 0 on success, non-zero on failure.
static int pixelBufferToPlanes(CVPixelBufferRef pixBuf,
                                int *outW, int *outH,
                                uint8_t *yDst, int yDstLen,
                                uint8_t *uvDst, int uvDstLen,
                                int *outYStride, int *outUVStride) {
    CVPixelBufferLockBaseAddress(pixBuf, kCVPixelBufferLock_ReadOnly);

    *outW = (int)CVPixelBufferGetWidth(pixBuf);
    *outH = (int)CVPixelBufferGetHeight(pixBuf);
    if (*outW <= 0 || *outH <= 0) {
        CVPixelBufferUnlockBaseAddress(pixBuf, kCVPixelBufferLock_ReadOnly);
        return -1;
    }

    uint8_t *yBase = (uint8_t *)CVPixelBufferGetBaseAddressOfPlane(pixBuf, 0);
    int yStride = (int)CVPixelBufferGetBytesPerRowOfPlane(pixBuf, 0);
    uint8_t *uvBase = (uint8_t *)CVPixelBufferGetBaseAddressOfPlane(pixBuf, 1);
    int uvStride = (int)CVPixelBufferGetBytesPerRowOfPlane(pixBuf, 1);

    if (!yBase || !uvBase) {
        CVPixelBufferUnlockBaseAddress(pixBuf, kCVPixelBufferLock_ReadOnly);
        return -2;
    }

    *outYStride = yStride;
    *outUVStride = uvStride;

    int h = *outH;
    int chromaH = (h + 1) / 2;

    // Copy Y plane row by row.
    int w = *outW;
    for (int row = 0; row < h && row * w < yDstLen; row++) {
        memcpy(yDst + row * w, yBase + row * yStride, w < yStride ? w : yStride);
    }

    // Copy interleaved UV plane row by row.
    int uvW = ((w + 1) / 2) * 2; // interleaved pairs
    for (int row = 0; row < chromaH && row * uvW < uvDstLen; row++) {
        memcpy(uvDst + row * uvW, uvBase + row * uvStride, uvW < uvStride ? uvW : uvStride);
    }

    CVPixelBufferUnlockBaseAddress(pixBuf, kCVPixelBufferLock_ReadOnly);
    return 0;
}
*/
import "C"
import (
	"bytes"
	"encoding/binary"
	"image"
	"unsafe"
)

// vtDecoder implements FrameDecoder using macOS VideoToolbox hardware H.264 decoding.
type vtDecoder struct {
	session    C.VTDecompressionSessionRef
	formatDesc C.CMVideoFormatDescriptionRef
	hasSession bool
	sps        []byte
	pps        []byte
}

func (d *vtDecoder) Decode(nalData []byte) *image.YCbCr {
	if len(nalData) == 0 {
		return nil
	}

	nalus := splitNALUs(nalData)
	if len(nalus) == 0 {
		return nil
	}

	// Extract SPS/PPS and rebuild session if changed.
	spsChanged := false
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		nalType := nalu[0] & 0x1F
		switch nalType {
		case 7: // SPS
			if !bytes.Equal(d.sps, nalu) {
				d.sps = append([]byte(nil), nalu...)
				spsChanged = true
			}
		case 8: // PPS
			if !bytes.Equal(d.pps, nalu) {
				d.pps = append([]byte(nil), nalu...)
				spsChanged = true
			}
		}
	}

	if spsChanged && d.hasSession {
		d.destroySession()
	}

	// Create session lazily once we have SPS+PPS.
	if !d.hasSession {
		if d.sps == nil || d.pps == nil {
			return nil
		}
		if !d.createSession() {
			return nil
		}
	}

	// Build AVCC data: concatenate all non-parameter NALUs with 4-byte length prefix.
	avcc := nalusToAVCC(nalus)
	if len(avcc) == 0 {
		return nil
	}

	// Decode synchronously.
	var ctx C.VTDecodeContext
	status := C.decodeSample(d.session, d.formatDesc,
		(*C.uint8_t)(unsafe.Pointer(&avcc[0])), C.size_t(len(avcc)), &ctx)
	if status != 0 || ctx.outputImage == nil {
		return nil
	}
	defer C.CVPixelBufferRelease(ctx.outputImage)

	return d.pixelBufferToImage(ctx.outputImage)
}

func (d *vtDecoder) Flush() *image.YCbCr {
	if !d.hasSession {
		return nil
	}
	C.VTDecompressionSessionWaitForAsynchronousFrames(d.session)
	return nil
}

func (d *vtDecoder) Close() {
	d.destroySession()
}

func (d *vtDecoder) createSession() bool {
	spsPtr := (*C.uint8_t)(unsafe.Pointer(&d.sps[0]))
	ppsPtr := (*C.uint8_t)(unsafe.Pointer(&d.pps[0]))

	status := C.createFormatDescription(spsPtr, C.size_t(len(d.sps)),
		ppsPtr, C.size_t(len(d.pps)), &d.formatDesc)
	if status != 0 {
		return false
	}

	status = C.createDecompSession(d.formatDesc, &d.session)
	if status != 0 {
		C.CFRelease(C.CFTypeRef(d.formatDesc))
		d.hasSession = false
		return false
	}
	d.hasSession = true
	return true
}

func (d *vtDecoder) destroySession() {
	if d.hasSession {
		C.destroySession(d.session, d.formatDesc)
		d.hasSession = false
	}
}

func (d *vtDecoder) pixelBufferToImage(pixBuf C.CVImageBufferRef) *image.YCbCr {
	var w, h C.int
	var yStride, uvStride C.int

	// Allocate max-size buffers based on a preliminary size query.
	pw := int(C.CVPixelBufferGetWidth(pixBuf))
	ph := int(C.CVPixelBufferGetHeight(pixBuf))
	if pw <= 0 || ph <= 0 {
		return nil
	}

	chromaW := (pw + 1) / 2
	chromaH := (ph + 1) / 2

	yBuf := make([]byte, pw*ph)
	uvBuf := make([]byte, chromaW*2*chromaH)

	ret := C.pixelBufferToPlanes(pixBuf,
		&w, &h,
		(*C.uint8_t)(unsafe.Pointer(&yBuf[0])), C.int(len(yBuf)),
		(*C.uint8_t)(unsafe.Pointer(&uvBuf[0])), C.int(len(uvBuf)),
		&yStride, &uvStride)
	if ret != 0 {
		return nil
	}

	imgW := int(w)
	imgH := int(h)
	cW := (imgW + 1) / 2
	cH := (imgH + 1) / 2

	// Deinterleave NV12 CbCr into separate Cb and Cr planes.
	cbData := make([]byte, cW*cH)
	crData := make([]byte, cW*cH)
	for row := range cH {
		srcOff := row * cW * 2
		dstOff := row * cW
		for col := range cW {
			cbData[dstOff+col] = uvBuf[srcOff+col*2]
			crData[dstOff+col] = uvBuf[srcOff+col*2+1]
		}
	}

	return &image.YCbCr{
		Y:              yBuf[:imgW*imgH],
		Cb:             cbData,
		Cr:             crData,
		YStride:        imgW,
		CStride:        cW,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, 0, imgW, imgH),
	}
}

// splitNALUs splits an Annex B byte stream on 0x00000001 start codes.
func splitNALUs(data []byte) [][]byte {
	startCode := []byte{0x00, 0x00, 0x00, 0x01}
	var nalus [][]byte
	for {
		idx := bytes.Index(data, startCode)
		if idx < 0 {
			if len(data) > 0 {
				nalus = append(nalus, data)
			}
			break
		}
		if idx > 0 {
			nalus = append(nalus, data[:idx])
		}
		data = data[idx+4:]
	}
	return nalus
}

// nalusToAVCC converts NAL units to AVCC format (4-byte big-endian length prefix),
// skipping SPS (7) and PPS (8) since they're in the format description.
func nalusToAVCC(nalus [][]byte) []byte {
	var buf []byte
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		nalType := nalu[0] & 0x1F
		if nalType == 7 || nalType == 8 {
			continue
		}
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nalu)))
		buf = append(buf, lenBuf[:]...)
		buf = append(buf, nalu...)
	}
	return buf
}
