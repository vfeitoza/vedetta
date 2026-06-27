//go:build linux && cgo && nvdec

package media

/*
#cgo pkg-config: libavcodec libavutil

// This code references only libavutil symbols (av_hwdevice_ctx_create with
// AV_HWDEVICE_TYPE_CUDA, the AV_PIX_FMT_CUDA enum, av_frame_*). libavutil loads
// the CUDA driver (libcuda) dynamically at runtime, so the build needs no CUDA
// toolkit and must NOT link -lcuda. The NVIDIA driver only has to be present at
// runtime on the host that actually decodes.
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_cuda.h>
#include <stdlib.h>

static AVBufferRef* nvdec_create_hwdevice() {
    AVBufferRef *hw_device_ctx = NULL;
    if (av_hwdevice_ctx_create(&hw_device_ctx, AV_HWDEVICE_TYPE_CUDA, NULL, NULL, 0) < 0) {
        return NULL;
    }
    return hw_device_ctx;
}

static enum AVPixelFormat nvdec_get_format(AVCodecContext *ctx, const enum AVPixelFormat *pix_fmts) {
    (void)ctx;
    for (const enum AVPixelFormat *p = pix_fmts; *p != AV_PIX_FMT_NONE; p++) {
        if (*p == AV_PIX_FMT_CUDA) return AV_PIX_FMT_CUDA;
    }
    return AV_PIX_FMT_NONE;
}

static void nvdec_set_get_format(AVCodecContext *ctx) {
    ctx->get_format = nvdec_get_format;
}
*/
import "C"

import (
	"errors"
	"image"
	"unsafe"
)

type nvdecDecoder struct {
	codecCtx    *C.AVCodecContext
	hwDeviceCtx *C.AVBufferRef
	pkt         *C.AVPacket
	frame       *C.AVFrame
	swFrame     *C.AVFrame
}

func probeNVDEC() bool {
	ctx := C.nvdec_create_hwdevice()
	if ctx == nil {
		return false
	}
	C.av_buffer_unref(&ctx)
	return true
}

func newNVDECDecoder() (*nvdecDecoder, error) {
	hwCtx := C.nvdec_create_hwdevice()
	if hwCtx == nil {
		return nil, errors.New("nvdec: failed to create CUDA device context")
	}

	codec := C.avcodec_find_decoder(C.AV_CODEC_ID_H264)
	if codec == nil {
		C.av_buffer_unref(&hwCtx)
		return nil, errors.New("nvdec: h264 decoder not found")
	}

	codecCtx := C.avcodec_alloc_context3(codec)
	if codecCtx == nil {
		C.av_buffer_unref(&hwCtx)
		return nil, errors.New("nvdec: alloc context failed")
	}

	codecCtx.hw_device_ctx = C.av_buffer_ref(hwCtx)
	C.nvdec_set_get_format(codecCtx)

	if C.avcodec_open2(codecCtx, codec, nil) < 0 {
		C.avcodec_free_context(&codecCtx)
		C.av_buffer_unref(&hwCtx)
		return nil, errors.New("nvdec: open codec failed")
	}

	return &nvdecDecoder{
		codecCtx:    codecCtx,
		hwDeviceCtx: hwCtx,
		pkt:         C.av_packet_alloc(),
		frame:       C.av_frame_alloc(),
		swFrame:     C.av_frame_alloc(),
	}, nil
}

func (d *nvdecDecoder) Decode(nalData []byte) *image.YCbCr {
	if len(nalData) == 0 {
		return nil
	}

	d.pkt.data = (*C.uint8_t)(unsafe.Pointer(&nalData[0]))
	d.pkt.size = C.int(len(nalData))

	if C.avcodec_send_packet(d.codecCtx, d.pkt) < 0 {
		return nil
	}

	if C.avcodec_receive_frame(d.codecCtx, d.frame) < 0 {
		return nil
	}

	d.swFrame.format = C.int(C.AV_PIX_FMT_NV12)
	if C.av_hwframe_transfer_data(d.swFrame, d.frame, 0) < 0 {
		C.av_frame_unref(d.frame)
		return nil
	}
	C.av_frame_unref(d.frame)

	img := d.extractNV12()
	C.av_frame_unref(d.swFrame)
	return img
}

func (d *nvdecDecoder) Flush() *image.YCbCr {
	if C.avcodec_send_packet(d.codecCtx, nil) < 0 {
		return nil
	}
	if C.avcodec_receive_frame(d.codecCtx, d.frame) < 0 {
		return nil
	}

	d.swFrame.format = C.int(C.AV_PIX_FMT_NV12)
	if C.av_hwframe_transfer_data(d.swFrame, d.frame, 0) < 0 {
		C.av_frame_unref(d.frame)
		return nil
	}
	C.av_frame_unref(d.frame)

	img := d.extractNV12()
	C.av_frame_unref(d.swFrame)
	return img
}

func (d *nvdecDecoder) extractNV12() *image.YCbCr {
	w := int(d.swFrame.width)
	h := int(d.swFrame.height)
	yStride := int(d.swFrame.linesize[0])
	uvStride := int(d.swFrame.linesize[1])

	ySize := yStride * h
	uvSize := uvStride * (h / 2)

	yData := C.GoBytes(unsafe.Pointer(d.swFrame.data[0]), C.int(ySize))
	uvData := C.GoBytes(unsafe.Pointer(d.swFrame.data[1]), C.int(uvSize))

	return nv12PlanesToYCbCr(w, h, yStride, uvStride, yData, uvData)
}

func (d *nvdecDecoder) Close() {
	if d.pkt != nil {
		C.av_packet_free(&d.pkt)
	}
	if d.frame != nil {
		C.av_frame_free(&d.frame)
	}
	if d.swFrame != nil {
		C.av_frame_free(&d.swFrame)
	}
	if d.codecCtx != nil {
		C.avcodec_free_context(&d.codecCtx)
	}
	if d.hwDeviceCtx != nil {
		C.av_buffer_unref(&d.hwDeviceCtx)
	}
}

// nvdecAvailable and newNVDECBackend are the hooks the Linux factory dispatches
// through. They exist only in this -tags nvdec build; decoder_nvdec_stub.go
// provides no-op versions otherwise.
func nvdecAvailable() bool { return probeNVDEC() }

func newNVDECBackend() (FrameDecoder, error) { return newNVDECDecoder() }
