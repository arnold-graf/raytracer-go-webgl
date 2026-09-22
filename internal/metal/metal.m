// Metal backend: the same megakernel, with Apple's intersector in place of the
// WGSL BVH traversal. The shader is generated from the same WGSL the portable
// backend runs (see gen.sh) and the scene bytes come from the same packer
// (internal/webgpu.Packer), so traversal is the only thing that differs.
//
// Compiled without ARC, as cgo does: every object stored past its autorelease
// pool is CFRetain'd and released in mtl_free.

#import <Metal/Metal.h>
#import <Foundation/Foundation.h>
#include <string.h>
#include "metal.h"

#define MAX_BUFFERS 40
#define MAX_BLAS 64
#define MAX_KERNELS 12

// naga numbers a kernel's arguments in signature order, so the same buffer sits
// at a different index in main_ than in aa_resolve; each kernel carries its own
// map from Metal argument index to WGSL binding.
//
// Intersection function tables are created from a pipeline and their handles
// are valid only for that pipeline, so each tracing kernel owns its own tables
// and its own argument buffer. Sharing them leaves the others dispatching to
// nothing, and every ray silently misses.
typedef struct {
    id<MTLComputePipelineState> pipe;
    int map[MAX_BUFFERS];
    int nmap, tx, ty, indirect;
    int tg_bytes[8], n_tg;
    id<MTLIntersectionFunctionTable> geom_table, block_table;
    id<MTLBuffer> handle;
    int rt_handle_idx;
} Kernel;

typedef struct {
    id<MTLAccelerationStructure> as;
    id<MTLBuffer> boxes, pdata;
    int count;
} Blas;

struct MTLBackend {
    id<MTLDevice> dev;
    id<MTLCommandQueue> queue;
    id<MTLLibrary> lib;

    id<MTLBuffer> buffers[MAX_BUFFERS];
    Kernel kernels[MAX_KERNELS];
    int n_kernels;

    int n_geom, n_blocker, n_inst, inst_written;
    Blas geom[MAX_BLAS], block[MAX_BLAS];
    id<MTLBuffer> inst_desc;
    id<MTLAccelerationStructure> geom_tlas, block_tlas;
    id<MTLBuffer> refit_scratch;
    size_t refit_scratch_len;
};

static void set_err(char *err, int errn, NSString *s) {
    if (err && errn > 0) snprintf(err, errn, "%s", [s UTF8String]);
}

static id keep(id obj) {
    if (obj) CFRetain((__bridge CFTypeRef)obj);
    return obj;
}

int mtl_supported(void) {
    @autoreleasepool {
        id<MTLDevice> d = MTLCreateSystemDefaultDevice();
        return (d && [d supportsRaytracing]) ? 1 : 0;
    }
}

MTLBackend *mtl_new(const void *lib, size_t lib_len, char *err, int errn) {
    @autoreleasepool {
        MTLBackend *b = calloc(1, sizeof(MTLBackend));
        b->dev = keep(MTLCreateSystemDefaultDevice());
        if (!b->dev) { set_err(err, errn, @"no Metal device"); free(b); return NULL; }
        if (![b->dev supportsRaytracing]) {
            set_err(err, errn, @"device does not support raytracing");
            free(b); return NULL;
        }
        b->queue = keep([b->dev newCommandQueue]);
        NSError *e = nil;
        dispatch_data_t dd = dispatch_data_create(lib, lib_len, NULL,
                                                  DISPATCH_DATA_DESTRUCTOR_DEFAULT);
        b->lib = keep([b->dev newLibraryWithData:dd error:&e]);
        if (!b->lib) {
            set_err(err, errn, [NSString stringWithFormat:@"load library: %@", e]);
            free(b); return NULL;
        }
        return b;
    }
}

void mtl_free(MTLBackend *b) {
    if (!b) return;
    @autoreleasepool {
        for (int i = 0; i < MAX_BUFFERS; i++)
            if (b->buffers[i]) CFRelease((__bridge CFTypeRef)b->buffers[i]);
        for (int i = 0; i < b->n_kernels; i++) {
            Kernel *k = &b->kernels[i];
            if (k->pipe) CFRelease((__bridge CFTypeRef)k->pipe);
            if (k->geom_table) CFRelease((__bridge CFTypeRef)k->geom_table);
            if (k->block_table) CFRelease((__bridge CFTypeRef)k->block_table);
            if (k->handle) CFRelease((__bridge CFTypeRef)k->handle);
        }
        for (int i = 0; i < b->n_geom; i++) {
            if (b->geom[i].as) CFRelease((__bridge CFTypeRef)b->geom[i].as);
            if (b->geom[i].boxes) CFRelease((__bridge CFTypeRef)b->geom[i].boxes);
            if (b->geom[i].pdata) CFRelease((__bridge CFTypeRef)b->geom[i].pdata);
        }
        for (int i = 0; i < b->n_blocker; i++) {
            if (b->block[i].as) CFRelease((__bridge CFTypeRef)b->block[i].as);
            if (b->block[i].boxes) CFRelease((__bridge CFTypeRef)b->block[i].boxes);
            if (b->block[i].pdata) CFRelease((__bridge CFTypeRef)b->block[i].pdata);
        }
        if (b->inst_desc) CFRelease((__bridge CFTypeRef)b->inst_desc);
        if (b->geom_tlas) CFRelease((__bridge CFTypeRef)b->geom_tlas);
        if (b->block_tlas) CFRelease((__bridge CFTypeRef)b->block_tlas);
        if (b->refit_scratch) CFRelease((__bridge CFTypeRef)b->refit_scratch);
        if (b->lib) CFRelease((__bridge CFTypeRef)b->lib);
        if (b->queue) CFRelease((__bridge CFTypeRef)b->queue);
        if (b->dev) CFRelease((__bridge CFTypeRef)b->dev);
        free(b);
    }
}

int mtl_alloc(MTLBackend *b, int binding, size_t size) {
    if (binding < 0 || binding >= MAX_BUFFERS) return 0;
    @autoreleasepool {
        if (b->buffers[binding]) CFRelease((__bridge CFTypeRef)b->buffers[binding]);
        b->buffers[binding] = keep([b->dev newBufferWithLength:(size ? size : 16)
                                                      options:MTLResourceStorageModeShared]);
        if (!b->buffers[binding]) return 0;
        memset([b->buffers[binding] contents], 0, size ? size : 16);
        return 1;
    }
}

int mtl_write(MTLBackend *b, int binding, const void *data, size_t len) {
    if (binding < 0 || binding >= MAX_BUFFERS || !b->buffers[binding]) return 0;
    if (len > [b->buffers[binding] length]) return 0;
    memcpy([b->buffers[binding] contents], data, len);
    return 1;
}

int mtl_zero(MTLBackend *b, int binding, size_t len) {
    if (binding < 0 || binding >= MAX_BUFFERS || !b->buffers[binding]) return 0;
    if (len > [b->buffers[binding] length]) len = [b->buffers[binding] length];
    memset([b->buffers[binding] contents], 0, len);
    return 1;
}

int mtl_read(MTLBackend *b, int binding, void *dst, size_t len) {
    if (binding < 0 || binding >= MAX_BUFFERS || !b->buffers[binding]) return 0;
    if (len > [b->buffers[binding] length]) return 0;
    memcpy(dst, [b->buffers[binding] contents], len);
    return 1;
}

int mtl_accel_begin(MTLBackend *b, int n_geom, int n_blocker, int n_inst) {
    if (n_geom > MAX_BLAS || n_blocker > MAX_BLAS) return 0;
    @autoreleasepool {
        for (int i = 0; i < b->n_geom; i++) {
            if (b->geom[i].as) CFRelease((__bridge CFTypeRef)b->geom[i].as);
            if (b->geom[i].boxes) CFRelease((__bridge CFTypeRef)b->geom[i].boxes);
            if (b->geom[i].pdata) CFRelease((__bridge CFTypeRef)b->geom[i].pdata);
        }
        for (int i = 0; i < b->n_blocker; i++) {
            if (b->block[i].as) CFRelease((__bridge CFTypeRef)b->block[i].as);
            if (b->block[i].boxes) CFRelease((__bridge CFTypeRef)b->block[i].boxes);
            if (b->block[i].pdata) CFRelease((__bridge CFTypeRef)b->block[i].pdata);
        }
        memset(b->geom, 0, sizeof(b->geom));
        memset(b->block, 0, sizeof(b->block));
        b->n_geom = n_geom; b->n_blocker = n_blocker; b->n_inst = n_inst;
        b->inst_written = 0;
        if (b->inst_desc) CFRelease((__bridge CFTypeRef)b->inst_desc);
        size_t sz = sizeof(MTLAccelerationStructureInstanceDescriptor) * (size_t)(n_inst ? n_inst : 1);
        b->inst_desc = keep([b->dev newBufferWithLength:sz options:MTLResourceStorageModeShared]);
        return b->inst_desc != nil;
    }
}

// bounds is count * 6 floats; gidx carries absolute prims[]/blockers[] indices,
// which ride in the structure as primitive data because a bounding box
// intersection function may not read instance_id.
int mtl_accel_blas(MTLBackend *b, int blocker, int slot, const float *bounds,
                   const uint32_t *gidx, int count) {
    if (slot < 0 || slot >= MAX_BLAS) return 0;
    Blas *dst = blocker ? &b->block[slot] : &b->geom[slot];
    @autoreleasepool {
        int n = count > 0 ? count : 1;
        if (!dst->boxes || dst->count != count) {
            if (dst->boxes) CFRelease((__bridge CFTypeRef)dst->boxes);
            if (dst->pdata) CFRelease((__bridge CFTypeRef)dst->pdata);
            dst->boxes = keep([b->dev newBufferWithLength:sizeof(MTLAxisAlignedBoundingBox) * n
                                                  options:MTLResourceStorageModeShared]);
            dst->pdata = keep([b->dev newBufferWithLength:sizeof(uint32_t) * n
                                                  options:MTLResourceStorageModeShared]);
            if (!dst->boxes || !dst->pdata) return 0;
        }
        MTLAxisAlignedBoundingBox *bb = (MTLAxisAlignedBoundingBox *)[dst->boxes contents];
        uint32_t *pd = (uint32_t *)[dst->pdata contents];
        for (int i = 0; i < count; i++) {
            bb[i].min = (MTLPackedFloat3){ bounds[i*6+0], bounds[i*6+1], bounds[i*6+2] };
            bb[i].max = (MTLPackedFloat3){ bounds[i*6+3], bounds[i*6+4], bounds[i*6+5] };
            pd[i] = gidx[i];
        }
        dst->count = count;
        return 1;
    }
}

// xf12 is object->world, three rows of four. MTLPackedFloat4x3 is four columns
// of three, so this transposes; getting it backwards places every instance at
// the inverse of where it belongs.
int mtl_accel_instance(MTLBackend *b, const float *xf12, uint32_t blas) {
    if (b->inst_written >= b->n_inst) return 0;
    MTLAccelerationStructureInstanceDescriptor *d =
        (MTLAccelerationStructureInstanceDescriptor *)[b->inst_desc contents];
    d += b->inst_written;
    for (int c = 0; c < 4; c++)
        for (int r = 0; r < 3; r++)
            d->transformationMatrix.columns[c].elements[r] = xf12[r*4+c];
    // Not opaque: an opaque bounding box skips the intersection function, which
    // is the entire mechanism for procedural primitives.
    d->options = MTLAccelerationStructureInstanceOptionNone;
    d->mask = 0xFFFFFFFF;
    d->intersectionFunctionTableOffset = 0;
    d->accelerationStructureIndex = blas;
    b->inst_written++;
    return 1;
}

static MTLPrimitiveAccelerationStructureDescriptor *bbox_desc(Blas *bl) {
    MTLAccelerationStructureBoundingBoxGeometryDescriptor *g =
        [MTLAccelerationStructureBoundingBoxGeometryDescriptor descriptor];
    g.boundingBoxBuffer = bl->boxes;
    g.boundingBoxCount = bl->count;
    g.primitiveDataBuffer = bl->pdata;
    g.primitiveDataStride = sizeof(uint32_t);
    g.primitiveDataElementSize = sizeof(uint32_t);
    g.opaque = NO;
    MTLPrimitiveAccelerationStructureDescriptor *d =
        [MTLPrimitiveAccelerationStructureDescriptor descriptor];
    // Refit is only legal on a structure *built* for it. Without this flag the
    // build succeeds, the refit is accepted, and the GPU then runs until the
    // watchdog kills the command buffer -- which on this machine took the whole
    // window server down with it. The header is explicit: "Enable refitting for
    // this acceleration structure. Note that this may reduce acceleration
    // structure quality."
    d.usage = MTLAccelerationStructureUsageRefit;
    d.geometryDescriptors = @[g];
    return d;
}

static MTLInstanceAccelerationStructureDescriptor *inst_desc(MTLBackend *b, NSArray *blases) {
    MTLInstanceAccelerationStructureDescriptor *d =
        [MTLInstanceAccelerationStructureDescriptor descriptor];
    d.usage = MTLAccelerationStructureUsageRefit;
    d.instanceDescriptorType = MTLAccelerationStructureInstanceDescriptorTypeDefault;
    d.instanceDescriptorStride = sizeof(MTLAccelerationStructureInstanceDescriptor);
    d.instancedAccelerationStructures = blases;
    d.instanceCount = b->inst_written;
    d.instanceDescriptorBuffer = b->inst_desc;
    return d;
}

static id<MTLAccelerationStructure> build_one(MTLBackend *b, MTLAccelerationStructureDescriptor *desc) {
    MTLAccelerationStructureSizes sz = [b->dev accelerationStructureSizesWithDescriptor:desc];
    id<MTLAccelerationStructure> as = [b->dev newAccelerationStructureWithSize:sz.accelerationStructureSize];
    id<MTLBuffer> scratch = [b->dev newBufferWithLength:(sz.buildScratchBufferSize ? sz.buildScratchBufferSize : 16)
                                                options:MTLResourceStorageModePrivate];
    id<MTLCommandBuffer> cb = [b->queue commandBuffer];
    id<MTLAccelerationStructureCommandEncoder> enc = [cb accelerationStructureCommandEncoder];
    [enc buildAccelerationStructure:as descriptor:desc scratchBuffer:scratch scratchBufferOffset:0];
    [enc endEncoding];
    [cb commit];
    [cb waitUntilCompleted];
    if ([cb error]) return nil;
    return keep(as);
}

int mtl_accel_build(MTLBackend *b, char *err, int errn) {
    @autoreleasepool {
        NSMutableArray *g = [NSMutableArray array], *bl = [NSMutableArray array];
        for (int i = 0; i < b->n_geom; i++) {
            if (b->geom[i].as) CFRelease((__bridge CFTypeRef)b->geom[i].as);
            b->geom[i].as = build_one(b, bbox_desc(&b->geom[i]));
            if (!b->geom[i].as) { set_err(err, errn, @"geometry BLAS build failed"); return 0; }
            [g addObject:b->geom[i].as];
        }
        for (int i = 0; i < b->n_blocker; i++) {
            if (b->block[i].as) CFRelease((__bridge CFTypeRef)b->block[i].as);
            b->block[i].as = build_one(b, bbox_desc(&b->block[i]));
            if (!b->block[i].as) { set_err(err, errn, @"blocker BLAS build failed"); return 0; }
            [bl addObject:b->block[i].as];
        }
        if (b->geom_tlas) CFRelease((__bridge CFTypeRef)b->geom_tlas);
        if (b->block_tlas) CFRelease((__bridge CFTypeRef)b->block_tlas);
        b->geom_tlas = build_one(b, inst_desc(b, g));
        b->block_tlas = build_one(b, inst_desc(b, bl));
        if (!b->geom_tlas || !b->block_tlas) { set_err(err, errn, @"TLAS build failed"); return 0; }
        return 1;
    }
}

// mtl_accel_refit updates bounds in place, keeping topology -- the analogue of
// bvh_refit.go, and the same trade: cheap, and it degrades as geometry moves
// away from where the tree was built. The caller decides when a rebuild is due.
// mtl_accel_refit updates bounds in place, keeping topology -- the analogue of
// bvh_refit.go, and the same trade: cheap, and it degrades as geometry moves
// away from where the tree was built. The caller decides when a rebuild is due.
//
// Two things here are not optional, and getting either wrong wedges the GPU
// until the watchdog kills the command buffer (which surfaces as
// kIOGPUCommandBufferCallbackErrorImpactingInteractivity, and as a beachball):
//
//   - Every refit in flight needs its *own* scratch. Refits encoded together
//     may run concurrently, and one shared buffer means they overwrite each
//     other's working memory.
//   - The top level must be refit after the bottom level has finished, because
//     its bounds are derived from theirs. Separate encoders give that ordering;
//     commands within one encoder do not.
#define REFIT_SCRATCH_ALIGN 256

static size_t refit_need(MTLBackend *b, MTLAccelerationStructureDescriptor *d) {
    size_t n = [b->dev accelerationStructureSizesWithDescriptor:d].refitScratchBufferSize;
    if (n == 0) n = REFIT_SCRATCH_ALIGN;
    return (n + REFIT_SCRATCH_ALIGN - 1) / REFIT_SCRATCH_ALIGN * REFIT_SCRATCH_ALIGN;
}

int mtl_accel_refit(MTLBackend *b, char *err, int errn) {
    @autoreleasepool {
        if (!b->geom_tlas || !b->block_tlas) { set_err(err, errn, @"refit before build"); return 0; }
        NSMutableArray *g = [NSMutableArray array], *bl = [NSMutableArray array];
        for (int i = 0; i < b->n_geom; i++) [g addObject:b->geom[i].as];
        for (int i = 0; i < b->n_blocker; i++) [bl addObject:b->block[i].as];

        // Lay every concurrent refit out at its own offset in one buffer.
        size_t total = 0;
        size_t goff[MAX_BLAS], boff[MAX_BLAS];
        for (int i = 0; i < b->n_geom; i++) {
            goff[i] = total; total += refit_need(b, bbox_desc(&b->geom[i]));
        }
        for (int i = 0; i < b->n_blocker; i++) {
            boff[i] = total; total += refit_need(b, bbox_desc(&b->block[i]));
        }
        size_t tg_off = total; total += refit_need(b, inst_desc(b, g));
        size_t tb_off = total; total += refit_need(b, inst_desc(b, bl));

        if (total > b->refit_scratch_len) {
            if (b->refit_scratch) CFRelease((__bridge CFTypeRef)b->refit_scratch);
            b->refit_scratch = keep([b->dev newBufferWithLength:total
                                                       options:MTLResourceStorageModePrivate]);
            b->refit_scratch_len = total;
        }
        if (getenv("RT_METAL_REFIT_TRACE"))
            fprintf(stderr, "  refit: %d+%d blas, scratch=%zu\n", b->n_geom, b->n_blocker, total);

        const char *only = getenv("RT_METAL_REFIT_ONLY");
        int do_bot = !only || strcmp(only, "tlas") != 0;
        int do_top = !only || strcmp(only, "blas") != 0;
        id<MTLCommandBuffer> cb = [b->queue commandBuffer];
        id<MTLAccelerationStructureCommandEncoder> bot = [cb accelerationStructureCommandEncoder];
        if (do_bot)
        for (int i = 0; i < b->n_geom; i++)
            [bot refitAccelerationStructure:b->geom[i].as descriptor:bbox_desc(&b->geom[i])
                                destination:nil scratchBuffer:b->refit_scratch
                        scratchBufferOffset:goff[i]];
        if (do_bot)
        for (int i = 0; i < b->n_blocker; i++)
            [bot refitAccelerationStructure:b->block[i].as descriptor:bbox_desc(&b->block[i])
                                destination:nil scratchBuffer:b->refit_scratch
                        scratchBufferOffset:boff[i]];
        [bot endEncoding];

        id<MTLAccelerationStructureCommandEncoder> top = [cb accelerationStructureCommandEncoder];
        if (do_top) {
        [top refitAccelerationStructure:b->geom_tlas descriptor:inst_desc(b, g)
                            destination:nil scratchBuffer:b->refit_scratch
                    scratchBufferOffset:tg_off];
        [top refitAccelerationStructure:b->block_tlas descriptor:inst_desc(b, bl)
                            destination:nil scratchBuffer:b->refit_scratch
                    scratchBufferOffset:tb_off];
        }
        [top endEncoding];

        [cb commit];
        [cb waitUntilCompleted];
        if ([cb error]) {
            set_err(err, errn, [NSString stringWithFormat:@"refit: %@", [cb error]]);
            return 0;
        }
        return 1;
    }
}

int mtl_kernel(MTLBackend *b, const char *name, const int *map, int nmap,
               int tx, int ty, int indirect, const int *tg_bytes, int n_tg,
               int traces, int prims_b, int blockers_b, int holes_b,
               int sizes_b, int rt_handle_idx, char *err, int errn) {
    @autoreleasepool {
        if (b->n_kernels >= MAX_KERNELS) { set_err(err, errn, @"too many kernels"); return 0; }
        NSError *e = nil;
        id<MTLFunction> kf = [b->lib newFunctionWithName:[NSString stringWithUTF8String:name]];
        if (!kf) { set_err(err, errn, [NSString stringWithFormat:@"no kernel %s", name]); return 0; }
        id<MTLFunction> pf = traces ? [b->lib newFunctionWithName:@"prim_isect"] : nil;
        id<MTLFunction> bf = traces ? [b->lib newFunctionWithName:@"blocker_isect"] : nil;

        MTLComputePipelineDescriptor *pd = [[MTLComputePipelineDescriptor alloc] init];
        pd.computeFunction = kf;
        if (pf && bf) {
            MTLLinkedFunctions *lf = [MTLLinkedFunctions linkedFunctions];
            lf.functions = @[pf, bf];
            pd.linkedFunctions = lf;
        }
        id<MTLComputePipelineState> ps =
            [b->dev newComputePipelineStateWithDescriptor:pd options:MTLPipelineOptionNone
                                               reflection:nil error:&e];
        if (!ps) { set_err(err, errn, [NSString stringWithFormat:@"pipeline %s: %@", name, e]); return 0; }

        Kernel *k = &b->kernels[b->n_kernels++];
        k->pipe = keep(ps); k->nmap = nmap; k->tx = tx; k->ty = ty; k->indirect = indirect;
        for (int i = 0; i < MAX_BUFFERS; i++) k->map[i] = -2;
        for (int i = 0; i < nmap && i < MAX_BUFFERS; i++) k->map[i] = map[i];
        k->n_tg = n_tg > 8 ? 8 : n_tg;
        for (int i = 0; i < k->n_tg; i++) k->tg_bytes[i] = tg_bytes[i];

        if (pf && bf) {
            MTLIntersectionFunctionTableDescriptor *td =
                [MTLIntersectionFunctionTableDescriptor intersectionFunctionTableDescriptor];
            td.functionCount = 1;
            k->geom_table = keep([ps newIntersectionFunctionTableWithDescriptor:td]);
            k->block_table = keep([ps newIntersectionFunctionTableWithDescriptor:td]);
            id<MTLFunctionHandle> ph = [ps functionHandleWithFunction:pf];
            id<MTLFunctionHandle> bh = [ps functionHandleWithFunction:bf];
            if (!ph || !bh) { set_err(err, errn, @"intersection function not linked"); return 0; }
            [k->geom_table setFunction:ph atIndex:0];
            [k->block_table setFunction:bh atIndex:0];
            [k->geom_table setBuffer:b->buffers[prims_b] offset:0 atIndex:0];
            [k->geom_table setBuffer:b->buffers[holes_b] offset:0 atIndex:1];
            [k->geom_table setBuffer:b->buffers[sizes_b] offset:0 atIndex:2];
            [k->block_table setBuffer:b->buffers[blockers_b] offset:0 atIndex:0];
            [k->block_table setBuffer:b->buffers[holes_b] offset:0 atIndex:1];
            [k->block_table setBuffer:b->buffers[sizes_b] offset:0 atIndex:2];

            id<MTLArgumentEncoder> ae = [kf newArgumentEncoderWithBufferIndex:rt_handle_idx];
            if (!ae) { set_err(err, errn, @"no argument encoder for rt_handle"); return 0; }
            k->handle = keep([b->dev newBufferWithLength:[ae encodedLength]
                                                 options:MTLResourceStorageModeShared]);
            k->rt_handle_idx = rt_handle_idx;
        }
        return 1;
    }
}

// The acceleration structures are rebuilt between frames, so the argument
// buffer's resource IDs have to be refreshed before each frame rather than
// written once at kernel creation.
static void refresh_handles(MTLBackend *b) {
    @autoreleasepool {
        for (int i = 0; i < b->n_kernels; i++) {
            Kernel *k = &b->kernels[i];
            if (!k->handle) continue;
            id<MTLFunction> kf = nil; // encoder is cheap to recreate from the pipeline's function
            (void)kf;
            uint64_t *h = (uint64_t *)[k->handle contents];
            MTLResourceID g = [b->geom_tlas gpuResourceID];
            MTLResourceID bl = [b->block_tlas gpuResourceID];
            MTLResourceID gt = [k->geom_table gpuResourceID];
            MTLResourceID bt = [k->block_table gpuResourceID];
            memcpy(&h[0], &g, 8);
            memcpy(&h[1], &bl, 8);
            memcpy(&h[2], &gt, 8);
            memcpy(&h[3], &bt, 8);
        }
    }
}

static void bind_kernel(MTLBackend *b, id<MTLComputeCommandEncoder> enc, Kernel *k) {
    [enc setComputePipelineState:k->pipe];
    for (int i = 0; i < k->nmap; i++) {
        int bi = k->map[i];
        if (bi >= 0 && bi < MAX_BUFFERS && b->buffers[bi])
            [enc setBuffer:b->buffers[bi] offset:0 atIndex:i];
    }
    if (k->handle) [enc setBuffer:k->handle offset:0 atIndex:k->rt_handle_idx];
    // Threadgroup memory is not allocated by declaring it; unsized, every
    // shared read returns zero and the kernel fails silently.
    for (int i = 0; i < k->n_tg; i++)
        [enc setThreadgroupMemoryLength:(k->tg_bytes[i] < 16 ? 16 : k->tg_bytes[i]) atIndex:i];
}

static void make_resident(MTLBackend *b, id<MTLComputeCommandEncoder> enc) {
    if (b->geom_tlas) [enc useResource:b->geom_tlas usage:MTLResourceUsageRead];
    if (b->block_tlas) [enc useResource:b->block_tlas usage:MTLResourceUsageRead];
    for (int i = 0; i < b->n_kernels; i++) {
        if (b->kernels[i].geom_table) [enc useResource:b->kernels[i].geom_table usage:MTLResourceUsageRead];
        if (b->kernels[i].block_table) [enc useResource:b->kernels[i].block_table usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < b->n_geom; i++) {
        [enc useResource:b->geom[i].as usage:MTLResourceUsageRead];
        [enc useResource:b->geom[i].pdata usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < b->n_blocker; i++) {
        [enc useResource:b->block[i].as usage:MTLResourceUsageRead];
        [enc useResource:b->block[i].pdata usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < MAX_BUFFERS; i++)
        if (b->buffers[i])
            [enc useResource:b->buffers[i] usage:MTLResourceUsageRead | MTLResourceUsageWrite];
}

// enabled[i] gates kernel i for this frame, mirroring the conditionals in
// internal/webgpu's submitTrace: a scene with no glossy surfaces must not run
// the reflection filter, and a view with AA off must not classify or resolve.
// Running them anyway is not just wasted time -- a pass that writes pixels when
// the portable backend would not have run it changes the frame.
int mtl_frame(MTLBackend *b, int gx, int gy, const int *enabled, char *err, int errn) {
    @autoreleasepool {
        refresh_handles(b);
        id<MTLCommandBuffer> cb = [b->queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = nil;
        for (int i = 0; i < b->n_kernels; i++) {
            if (enabled && !enabled[i]) continue;
            Kernel *k = &b->kernels[i];
            if (k->indirect >= 0) {
                if (enc) { [enc endEncoding]; enc = nil; }
                id<MTLComputeCommandEncoder> ie = [cb computeCommandEncoder];
                bind_kernel(b, ie, k);
                make_resident(b, ie);
                [ie dispatchThreadgroupsWithIndirectBuffer:b->buffers[k->indirect]
                                      indirectBufferOffset:0
                                     threadsPerThreadgroup:MTLSizeMake(k->tx, k->ty, 1)];
                [ie endEncoding];
                continue;
            }
            if (!enc) { enc = [cb computeCommandEncoder]; make_resident(b, enc); }
            bind_kernel(b, enc, k);
            [enc dispatchThreadgroups:MTLSizeMake(gx, gy, 1)
                threadsPerThreadgroup:MTLSizeMake(k->tx, k->ty, 1)];
            [enc memoryBarrierWithScope:MTLBarrierScopeBuffers];
        }
        if (enc) [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
        if ([cb error]) {
            set_err(err, errn, [NSString stringWithFormat:@"frame: %@", [cb error]]);
            return 0;
        }
        return 1;
    }
}
