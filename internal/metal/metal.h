// C surface for the Metal backend. See metal.m, and renderer.go for the caller.
#ifndef RT_METAL_H
#define RT_METAL_H

#include <stddef.h>
#include <stdint.h>

typedef struct MTLBackend MTLBackend;

// The library is the embedded trace.metallib; see gen.sh.
MTLBackend *mtl_new(const void *lib, size_t lib_len, char *err, int errn);
void mtl_free(MTLBackend *b);
int mtl_supported(void);

// Buffers are keyed by WGSL binding and allocated once. mtl_write copies a
// frame's bytes in; bindings the frame does not touch keep what they had,
// which is what makes a static scene cost nothing to re-upload.
int mtl_alloc(MTLBackend *b, int binding, size_t size);
int mtl_write(MTLBackend *b, int binding, const void *data, size_t len);
int mtl_zero(MTLBackend *b, int binding, size_t len);
int mtl_read(MTLBackend *b, int binding, void *dst, size_t len);

// Acceleration structures. begin/add_blas/add_instance/build replaces
// everything; refit keeps the topology and updates bounds in place, which is
// the analogue of bvh_refit.go and is what a moving scene uses.
int mtl_accel_begin(MTLBackend *b, int n_geom, int n_blocker, int n_inst);
int mtl_accel_blas(MTLBackend *b, int blocker, int slot, const float *bounds,
                   const uint32_t *gidx, int count);
int mtl_accel_instance(MTLBackend *b, const float *xf12, uint32_t blas);
int mtl_accel_build(MTLBackend *b, char *err, int errn);
int mtl_accel_refit(MTLBackend *b, char *err, int errn);

// Kernels are added once, in dispatch order. map[i] is the WGSL binding for
// Metal argument i; indirect is the binding holding dispatch arguments, or -1.
int mtl_kernel(MTLBackend *b, const char *name, const int *map, int nmap,
               int tx, int ty, int indirect, const int *tg_bytes, int n_tg,
               int traces, int prims_b, int blockers_b, int holes_b,
               int sizes_b, int rt_handle_idx, char *err, int errn);

// Runs the whole chain once and blocks until it completes.
int mtl_frame(MTLBackend *b, int gx, int gy, char *err, int errn);

#endif
