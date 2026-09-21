// C surface for the Metal harness. See metalrt.m for the implementation and
// tools/metal-rt/harness/main.go for the caller.
#ifndef METALRT_H
#define METALRT_H

#include <stddef.h>
#include <stdint.h>

typedef struct MRT MRT;

MRT *mrt_new(const char *metallib, char *err, int errn);
void mrt_free(MRT *m);
void mrt_set_verbose(int v);

// Buffers are bound by Metal index, which mslbind's manifest supplies.
// Allocations are padded: naga's runtime-array size table is set permissively
// (see main.go), so a bounds check that would have clamped can read past the
// data, and the padding keeps that inside a live allocation.
int mrt_set_buffer(MRT *m, int index, const void *data, size_t len, size_t pad);

// Acceleration structures. Geometry and blockers are separate sets, each with
// one structure for the static scene and one per instance template.
int mrt_begin_accel(MRT *m, int n_geom, int n_blocker, int n_inst);
int mrt_add_blas(MRT *m, int blocker, int slot, const float *bounds,
                 const uint32_t *gidx, int count);
int mrt_add_instance(MRT *m, const float *xf12, uint32_t blas);
int mrt_build_accel(MRT *m, char *err, int errn);

// Adds one kernel: its pipeline and how to bind it. map[i] is the WGSL binding
// for Metal argument i, -1 for naga's runtime-array size table, -2 unused.
// indirect is the WGSL binding holding dispatch arguments, or -1.
int mrt_add_kernel(MRT *m, const char *name, const int *map, int nmap,
                   int tx, int ty, int indirect, const int *tg_bytes, int n_tg,
                   int prims_b, int blockers_b, int holes_b, int sizes_b,
                   int rt_handle_idx, char *err, int errn);

// Runs every kernel added, in order, and returns the best frame time in ms.
double mrt_run(MRT *m, int gx, int gy, int iters,
               int reset_binding, const void *reset_data, size_t reset_len,
               char *err, int errn);

int mrt_read_buffer(MRT *m, int index, void *dst, size_t len);

// Traces n rays (origin xyz, direction xyz each) with the structures bound
// directly, to tell a broken structure apart from a broken argument buffer.
int mrt_selftest(MRT *m, uint32_t *blas_hit, uint32_t *tlas_hit, char *err, int errn);
int mrt_probe(MRT *m, const float *rays, int n, uint32_t *hits, int use_blas, char *err, int errn);

#endif
