// Metal side of the harness: build the acceleration structures, link the
// intersection functions, bind the buffers the WGSL backend uploaded, and
// dispatch the same megakernel.
//
// Nothing here knows anything about the scene. Every byte it binds came out of
// internal/webgpu's upload path (see bufdump.go) and every primitive it tests
// goes through naga's generated intersect(), so the only thing that differs
// between this and the WGSL backend is who walks the tree.

#import <Metal/Metal.h>
#import <Foundation/Foundation.h>
#include "metalrt.h"

#define MAX_BUFFERS 40
#define MAX_BLAS 64
#define MAX_KERNELS 12

// A kernel's arguments are numbered by naga in signature order, so the same
// buffer sits at a different index in main_ than in aa_resolve. Buffers are
// therefore keyed by WGSL binding and each kernel carries its own map.
typedef struct {
    id<MTLComputePipelineState> pipe;
    int map[MAX_BUFFERS];   // metal index -> WGSL binding, or -1 for the
                            // runtime-array size table, -2 for unused
    int nmap;
    int tx, ty;             // threads per threadgroup
    int indirect;           // WGSL binding holding dispatch args, or -1
    int tg_bytes[8];        // threadgroup allocations, by index
    int n_tg;
    // Intersection function tables are created *from a pipeline*, and the
    // function handles inside them are only valid for that pipeline. Sharing
    // one table across kernels leaves the others dispatching to nothing, so
    // every ray misses and the frame quietly renders the sky. Each tracing
    // kernel therefore gets its own tables and its own argument buffer, since
    // the buffer stores the tables' resource IDs.
    id<MTLIntersectionFunctionTable> geom_table, block_table;
    id<MTLBuffer> handle;
    int rt_handle_idx;
} Kernel;

struct MRT {
    id<MTLDevice> dev;
    id<MTLCommandQueue> queue;
    id<MTLLibrary> lib;
    id<MTLComputePipelineState> pipe;

    id<MTLBuffer> buffers[MAX_BUFFERS];   // by WGSL binding; SIZES_BINDING last
    Kernel kernels[MAX_KERNELS];
    int n_kernels;

    // Bottom-level structures, and the boxes/indices they are built from.
    int n_geom, n_blocker, n_inst;
    id<MTLAccelerationStructure> geom_blas[MAX_BLAS];
    id<MTLAccelerationStructure> block_blas[MAX_BLAS];
    id<MTLBuffer> geom_boxes[MAX_BLAS], geom_data[MAX_BLAS];
    id<MTLBuffer> block_boxes[MAX_BLAS], block_data[MAX_BLAS];
    int geom_count[MAX_BLAS], block_count[MAX_BLAS];

    id<MTLBuffer> inst_desc;   // MTLAccelerationStructureInstanceDescriptor[]
    int inst_written;
    id<MTLAccelerationStructure> geom_tlas, block_tlas;

    id<MTLIntersectionFunctionTable> geom_table, block_table;
    id<MTLBuffer> handle;      // the RTHandle argument buffer
    int rt_handle_idx;
};

static int mrt_verbose = 0;
void mrt_set_verbose(int v) { mrt_verbose = v; }

static void set_err(char *err, int errn, NSString *s) {
    if (err && errn > 0) snprintf(err, errn, "%s", [s UTF8String]);
}

MRT *mrt_new(const char *metallib, char *err, int errn) {
    @autoreleasepool {
        MRT *m = calloc(1, sizeof(MRT));
        m->dev = MTLCreateSystemDefaultDevice();
        if (!m->dev) { set_err(err, errn, @"no Metal device"); free(m); return NULL; }
        if (![m->dev supportsRaytracing]) {
            set_err(err, errn, @"device does not support raytracing");
            free(m); return NULL;
        }
        m->queue = [m->dev newCommandQueue];
        NSError *e = nil;
        NSURL *u = [NSURL fileURLWithPath:[NSString stringWithUTF8String:metallib]];
        m->lib = [m->dev newLibraryWithURL:u error:&e];
        if (!m->lib) {
            set_err(err, errn, [NSString stringWithFormat:@"load metallib: %@", e]);
            free(m); return NULL;
        }
        CFRetain((__bridge CFTypeRef)m->dev);
        CFRetain((__bridge CFTypeRef)m->queue);
        CFRetain((__bridge CFTypeRef)m->lib);
        return m;
    }
}

void mrt_free(MRT *m) { if (m) free(m); }

int mrt_set_buffer(MRT *m, int index, const void *data, size_t len, size_t pad) {
    if (index < 0 || index >= MAX_BUFFERS) return 0;
    @autoreleasepool {
        id<MTLBuffer> b = [m->dev newBufferWithLength:len + pad
                                              options:MTLResourceStorageModeShared];
        if (!b) return 0;
        if (data && len) memcpy([b contents], data, len);
        m->buffers[index] = b;
        CFRetain((__bridge CFTypeRef)b);
        return 1;
    }
}

int mrt_begin_accel(MRT *m, int n_geom, int n_blocker, int n_inst) {
    if (n_geom > MAX_BLAS || n_blocker > MAX_BLAS) return 0;
    m->n_geom = n_geom; m->n_blocker = n_blocker; m->n_inst = n_inst;
    m->inst_written = 0;
    @autoreleasepool {
        size_t sz = sizeof(MTLAccelerationStructureInstanceDescriptor) * (size_t)n_inst;
        m->inst_desc = [m->dev newBufferWithLength:sz options:MTLResourceStorageModeShared];
        if (!m->inst_desc) return 0;
        CFRetain((__bridge CFTypeRef)m->inst_desc);
        return 1;
    }
}

// bounds is count * 6 floats (min xyz, max xyz); gidx is count absolute indices
// into prims[]/blockers[], which ride along as per-primitive data because a
// bounding box intersection function cannot read instance_id.
int mrt_add_blas(MRT *m, int blocker, int slot, const float *bounds,
                 const uint32_t *gidx, int count) {
    if (slot < 0 || slot >= MAX_BLAS) return 0;
    @autoreleasepool {
        size_t bsz = sizeof(MTLAxisAlignedBoundingBox) * (size_t)(count > 0 ? count : 1);
        id<MTLBuffer> boxes = [m->dev newBufferWithLength:bsz options:MTLResourceStorageModeShared];
        id<MTLBuffer> pdata = [m->dev newBufferWithLength:sizeof(uint32_t) * (size_t)(count > 0 ? count : 1)
                                                  options:MTLResourceStorageModeShared];
        if (!boxes || !pdata) return 0;
        MTLAxisAlignedBoundingBox *bb = (MTLAxisAlignedBoundingBox *)[boxes contents];
        uint32_t *pd = (uint32_t *)[pdata contents];
        for (int i = 0; i < count; i++) {
            bb[i].min = (MTLPackedFloat3){ bounds[i*6+0], bounds[i*6+1], bounds[i*6+2] };
            bb[i].max = (MTLPackedFloat3){ bounds[i*6+3], bounds[i*6+4], bounds[i*6+5] };
            pd[i] = gidx[i];
        }
        if (mrt_verbose && slot == 0 && count > 0) {
            fprintf(stderr, "  %s slot0: sizeof(bbox)=%zu count=%d box[0]=(%.2f %.2f %.2f)..(%.2f %.2f %.2f) gidx=%u\n",
                    blocker ? "blocker" : "geom", sizeof(MTLAxisAlignedBoundingBox), count,
                    bb[0].min.x, bb[0].min.y, bb[0].min.z,
                    bb[0].max.x, bb[0].max.y, bb[0].max.z, pd[0]);
        }
        CFRetain((__bridge CFTypeRef)boxes);
        CFRetain((__bridge CFTypeRef)pdata);
        if (blocker) {
            m->block_boxes[slot] = boxes; m->block_data[slot] = pdata; m->block_count[slot] = count;
        } else {
            m->geom_boxes[slot] = boxes; m->geom_data[slot] = pdata; m->geom_count[slot] = count;
        }
        return 1;
    }
}

// xf12 is object->world, three rows of four. MTLPackedFloat4x3 is four columns
// of three, so this transposes on the way in; getting it backwards places every
// instance at the inverse of where it belongs.
int mrt_add_instance(MRT *m, const float *xf12, uint32_t blas) {
    if (m->inst_written >= m->n_inst) return 0;
    MTLAccelerationStructureInstanceDescriptor *d =
        (MTLAccelerationStructureInstanceDescriptor *)[m->inst_desc contents];
    d += m->inst_written;
    for (int c = 0; c < 4; c++)
        for (int r = 0; r < 3; r++)
            d->transformationMatrix.columns[c].elements[r] = xf12[r*4+c];
    d->options = MTLAccelerationStructureInstanceOptionNone;
    d->mask = 0xFFFFFFFF;
    d->intersectionFunctionTableOffset = 0;
    d->accelerationStructureIndex = blas;
    if (mrt_verbose && m->inst_written < 2) {
        fprintf(stderr, "  inst %d: sizeof=%zu blas=%u mask=%u cols=[(%.2f %.2f %.2f)(%.2f %.2f %.2f)(%.2f %.2f %.2f)(%.2f %.2f %.2f)]\n",
            m->inst_written, sizeof(MTLAccelerationStructureInstanceDescriptor), blas, d->mask,
            d->transformationMatrix.columns[0].elements[0], d->transformationMatrix.columns[0].elements[1], d->transformationMatrix.columns[0].elements[2],
            d->transformationMatrix.columns[1].elements[0], d->transformationMatrix.columns[1].elements[1], d->transformationMatrix.columns[1].elements[2],
            d->transformationMatrix.columns[2].elements[0], d->transformationMatrix.columns[2].elements[1], d->transformationMatrix.columns[2].elements[2],
            d->transformationMatrix.columns[3].elements[0], d->transformationMatrix.columns[3].elements[1], d->transformationMatrix.columns[3].elements[2]);
    }
    m->inst_written++;
    return 1;
}

static id<MTLAccelerationStructure> build_one(MRT *m, MTLAccelerationStructureDescriptor *desc) {
    MTLAccelerationStructureSizes sizes = [m->dev accelerationStructureSizesWithDescriptor:desc];
    if (mrt_verbose) fprintf(stderr, "  build: as=%zu scratch=%zu\n",
                             (size_t)sizes.accelerationStructureSize,
                             (size_t)sizes.buildScratchBufferSize);
    id<MTLAccelerationStructure> as = [m->dev newAccelerationStructureWithSize:sizes.accelerationStructureSize];
    id<MTLBuffer> scratch = [m->dev newBufferWithLength:(sizes.buildScratchBufferSize > 0 ? sizes.buildScratchBufferSize : 16)
                                                options:MTLResourceStorageModePrivate];
    id<MTLCommandBuffer> cb = [m->queue commandBuffer];
    id<MTLAccelerationStructureCommandEncoder> enc = [cb accelerationStructureCommandEncoder];
    [enc buildAccelerationStructure:as descriptor:desc scratchBuffer:scratch scratchBufferOffset:0];
    [enc endEncoding];
    [cb commit];
    [cb waitUntilCompleted];
    if ([cb error]) {
        fprintf(stderr, "  BUILD FAILED: %s\n",
                [[[cb error] localizedDescription] UTF8String]);
        return nil;
    }
    if (mrt_verbose) {
        // A descriptor Metal accepted but could not populate still returns a
        // structure, so report what actually went in.
        fprintf(stderr, "  built ok\n");
    }
    if (as) CFRetain((__bridge CFTypeRef)as);
    return as;
}

static MTLPrimitiveAccelerationStructureDescriptor *bbox_desc(id<MTLBuffer> boxes,
                                                              id<MTLBuffer> pdata,
                                                              int count) {
    MTLAccelerationStructureBoundingBoxGeometryDescriptor *g =
        [MTLAccelerationStructureBoundingBoxGeometryDescriptor descriptor];
    g.boundingBoxBuffer = boxes;
    g.boundingBoxCount = count;
    g.primitiveDataBuffer = pdata;
    g.primitiveDataStride = sizeof(uint32_t);
    g.primitiveDataElementSize = sizeof(uint32_t);
    g.opaque = NO;
    MTLPrimitiveAccelerationStructureDescriptor *d =
        [MTLPrimitiveAccelerationStructureDescriptor descriptor];
    d.geometryDescriptors = @[g];
    return d;
}

int mrt_build_accel(MRT *m, char *err, int errn) {
    @autoreleasepool {
        NSMutableArray *geom = [NSMutableArray array];
        for (int i = 0; i < m->n_geom; i++) {
            m->geom_blas[i] = build_one(m, bbox_desc(m->geom_boxes[i], m->geom_data[i], m->geom_count[i]));
            if (!m->geom_blas[i]) { set_err(err, errn, @"geometry BLAS build failed"); return 0; }
            [geom addObject:m->geom_blas[i]];
        }
        NSMutableArray *block = [NSMutableArray array];
        for (int i = 0; i < m->n_blocker; i++) {
            m->block_blas[i] = build_one(m, bbox_desc(m->block_boxes[i], m->block_data[i], m->block_count[i]));
            if (!m->block_blas[i]) { set_err(err, errn, @"blocker BLAS build failed"); return 0; }
            [block addObject:m->block_blas[i]];
        }
        MTLInstanceAccelerationStructureDescriptor *gd =
            [MTLInstanceAccelerationStructureDescriptor descriptor];
        gd.instanceDescriptorType = MTLAccelerationStructureInstanceDescriptorTypeDefault;
        gd.instanceDescriptorStride = sizeof(MTLAccelerationStructureInstanceDescriptor);
        gd.instancedAccelerationStructures = geom;
        gd.instanceCount = m->n_inst;
        gd.instanceDescriptorBuffer = m->inst_desc;
        m->geom_tlas = build_one(m, gd);

        MTLInstanceAccelerationStructureDescriptor *bd =
            [MTLInstanceAccelerationStructureDescriptor descriptor];
        bd.instanceDescriptorType = MTLAccelerationStructureInstanceDescriptorTypeDefault;
        bd.instanceDescriptorStride = sizeof(MTLAccelerationStructureInstanceDescriptor);
        bd.instancedAccelerationStructures = block;
        bd.instanceCount = m->n_inst;
        bd.instanceDescriptorBuffer = m->inst_desc;
        m->block_tlas = build_one(m, bd);

        if (!m->geom_tlas || !m->block_tlas) { set_err(err, errn, @"TLAS build failed"); return 0; }
        return 1;
    }
}

// mrt_add_kernel builds one pipeline and records how to bind it. The first
// kernel added also builds the intersection function tables, which every
// traversal-using kernel shares.
int mrt_add_kernel(MRT *m, const char *name, const int *map, int nmap,
                   int tx, int ty, int indirect, const int *tg_bytes, int n_tg,
                   int traces, int prims_b, int blockers_b, int holes_b,
                   int sizes_b, int rt_handle_idx, char *err, int errn) {
    @autoreleasepool {
        if (m->n_kernels >= MAX_KERNELS) { set_err(err, errn, @"too many kernels"); return 0; }
        NSError *e = nil;
        id<MTLFunction> kf = [m->lib newFunctionWithName:[NSString stringWithUTF8String:name]];
        if (!kf) { set_err(err, errn, [NSString stringWithFormat:@"no kernel %s", name]); return 0; }
        // Only the kernels that actually traverse declare rt_handle and need
        // the intersection functions linked; the screen-space filters do not.
        id<MTLFunction> pf = traces ? [m->lib newFunctionWithName:@"prim_isect"] : nil;
        id<MTLFunction> bf = traces ? [m->lib newFunctionWithName:@"blocker_isect"] : nil;

        MTLComputePipelineDescriptor *pd = [[MTLComputePipelineDescriptor alloc] init];
        pd.computeFunction = kf;
        // Only kernels that trace need the intersection functions linked, but
        // linking them everywhere is harmless and keeps one code path.
        if (pf && bf) {
            MTLLinkedFunctions *lf = [MTLLinkedFunctions linkedFunctions];
            lf.functions = @[pf, bf];
            pd.linkedFunctions = lf;
        }
        id<MTLComputePipelineState> ps =
            [m->dev newComputePipelineStateWithDescriptor:pd options:MTLPipelineOptionNone
                                               reflection:nil error:&e];
        if (!ps) { set_err(err, errn, [NSString stringWithFormat:@"pipeline %s: %@", name, e]); return 0; }
        CFRetain((__bridge CFTypeRef)ps);

        Kernel *k = &m->kernels[m->n_kernels++];
        k->pipe = ps; k->nmap = nmap; k->tx = tx; k->ty = ty; k->indirect = indirect;
        for (int i = 0; i < MAX_BUFFERS; i++) k->map[i] = -2;
        for (int i = 0; i < nmap && i < MAX_BUFFERS; i++) k->map[i] = map[i];
        k->n_tg = n_tg > 8 ? 8 : n_tg;
        for (int i = 0; i < k->n_tg; i++) k->tg_bytes[i] = tg_bytes[i];

        if (pf && bf) {
            MTLIntersectionFunctionTableDescriptor *td =
                [MTLIntersectionFunctionTableDescriptor intersectionFunctionTableDescriptor];
            td.functionCount = 1;
            k->geom_table = [ps newIntersectionFunctionTableWithDescriptor:td];
            k->block_table = [ps newIntersectionFunctionTableWithDescriptor:td];
            id<MTLFunctionHandle> ph = [ps functionHandleWithFunction:pf];
            id<MTLFunctionHandle> bh = [ps functionHandleWithFunction:bf];
            if (!ph || !bh) { set_err(err, errn, @"intersection function not linked"); return 0; }
            [k->geom_table setFunction:ph atIndex:0];
            [k->block_table setFunction:bh atIndex:0];
            [k->geom_table setBuffer:m->buffers[prims_b] offset:0 atIndex:0];
            [k->geom_table setBuffer:m->buffers[holes_b] offset:0 atIndex:1];
            [k->geom_table setBuffer:m->buffers[sizes_b] offset:0 atIndex:2];
            [k->block_table setBuffer:m->buffers[blockers_b] offset:0 atIndex:0];
            [k->block_table setBuffer:m->buffers[holes_b] offset:0 atIndex:1];
            [k->block_table setBuffer:m->buffers[sizes_b] offset:0 atIndex:2];
            CFRetain((__bridge CFTypeRef)k->geom_table);
            CFRetain((__bridge CFTypeRef)k->block_table);

            id<MTLArgumentEncoder> ae = [kf newArgumentEncoderWithBufferIndex:rt_handle_idx];
            if (!ae) { set_err(err, errn, @"no argument encoder for rt_handle"); return 0; }
            k->handle = [m->dev newBufferWithLength:[ae encodedLength]
                                            options:MTLResourceStorageModeShared];
            [ae setArgumentBuffer:k->handle offset:0];
            [ae setAccelerationStructure:m->geom_tlas atIndex:0];
            [ae setAccelerationStructure:m->block_tlas atIndex:1];
            [ae setIntersectionFunctionTable:k->geom_table atIndex:2];
            [ae setIntersectionFunctionTable:k->block_table atIndex:3];
            CFRetain((__bridge CFTypeRef)k->handle);
            k->rt_handle_idx = rt_handle_idx;
        }
        return 1;
    }
}

// bind_kernel sets one kernel's buffers and makes everything it reaches
// resident. Residency is explicit because the structures and tables arrive
// through an argument buffer, which the encoder cannot see into.
static void bind_kernel(MRT *m, id<MTLComputeCommandEncoder> enc, Kernel *k) {
    [enc setComputePipelineState:k->pipe];
    for (int i = 0; i < k->nmap; i++) {
        int b = k->map[i];
        if (b >= 0 && b < MAX_BUFFERS && m->buffers[b])
            [enc setBuffer:m->buffers[b] offset:0 atIndex:i];
    }
    if (k->handle) [enc setBuffer:k->handle offset:0 atIndex:k->rt_handle_idx];
    // Threadgroup memory is not allocated by declaring it; unsized, every
    // shared read returns zero and the kernel fails silently.
    for (int i = 0; i < k->n_tg; i++)
        [enc setThreadgroupMemoryLength:(k->tg_bytes[i] < 16 ? 16 : k->tg_bytes[i]) atIndex:i];
}

static void make_resident(MRT *m, id<MTLComputeCommandEncoder> enc) {
    if (m->geom_tlas) [enc useResource:m->geom_tlas usage:MTLResourceUsageRead];
    if (m->block_tlas) [enc useResource:m->block_tlas usage:MTLResourceUsageRead];
    for (int i = 0; i < m->n_kernels; i++) {
        if (m->kernels[i].geom_table)
            [enc useResource:m->kernels[i].geom_table usage:MTLResourceUsageRead];
        if (m->kernels[i].block_table)
            [enc useResource:m->kernels[i].block_table usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < m->n_geom; i++) {
        [enc useResource:m->geom_blas[i] usage:MTLResourceUsageRead];
        [enc useResource:m->geom_data[i] usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < m->n_blocker; i++) {
        [enc useResource:m->block_blas[i] usage:MTLResourceUsageRead];
        [enc useResource:m->block_data[i] usage:MTLResourceUsageRead];
    }
    for (int i = 0; i < MAX_BUFFERS; i++)
        if (m->buffers[i])
            [enc useResource:m->buffers[i] usage:MTLResourceUsageRead | MTLResourceUsageWrite];
}

// mrt_run encodes the whole frame: every kernel in the order they were added,
// in one encoder so each sees the previous one's writes, except any kernel
// dispatched indirectly -- those get their own encoder, because the buffer
// holding the dispatch arguments was written by the encoder before it.
double mrt_run(MRT *m, int gx, int gy, int iters,
               int reset_binding, const void *reset_data, size_t reset_len,
               char *err, int errn) {
    @autoreleasepool {
        double best = 1e30;
        for (int it = 0; it < iters; it++) {
            if (reset_binding >= 0 && m->buffers[reset_binding] && reset_data)
                memcpy([m->buffers[reset_binding] contents], reset_data, reset_len);

            id<MTLCommandBuffer> cb = [m->queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = nil;
            for (int i = 0; i < m->n_kernels; i++) {
                Kernel *k = &m->kernels[i];
                if (k->indirect >= 0) {
                    if (enc) { [enc endEncoding]; enc = nil; }
                    id<MTLComputeCommandEncoder> ie = [cb computeCommandEncoder];
                    bind_kernel(m, ie, k);
                    make_resident(m, ie);
                    [ie dispatchThreadgroupsWithIndirectBuffer:m->buffers[k->indirect]
                                          indirectBufferOffset:0
                                         threadsPerThreadgroup:MTLSizeMake(k->tx, k->ty, 1)];
                    [ie endEncoding];
                    continue;
                }
                if (!enc) {
                    enc = [cb computeCommandEncoder];
                    make_resident(m, enc);
                }
                bind_kernel(m, enc, k);
                [enc dispatchThreadgroups:MTLSizeMake(gx, gy, 1)
                    threadsPerThreadgroup:MTLSizeMake(k->tx, k->ty, 1)];
                // Each pass reads what the previous wrote.
                [enc memoryBarrierWithScope:MTLBarrierScopeBuffers];
            }
            if (enc) [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            if ([cb error]) {
                set_err(err, errn, [NSString stringWithFormat:@"dispatch: %@", [cb error]]);
                return -1;
            }
            double ms = ([cb GPUEndTime] - [cb GPUStartTime]) * 1000.0;
            if (ms < best) best = ms;
        }
        return best;
    }
}

int mrt_read_buffer(MRT *m, int index, void *dst, size_t len) {
    if (index < 0 || index >= MAX_BUFFERS || !m->buffers[index]) return 0;
    memcpy(dst, [m->buffers[index] contents], len);
    return 1;
}

// mrt_probe traces a handful of rays against the structures this harness just
// built, using a self-contained kernel that binds the acceleration structure
// and the function table *directly* rather than through an argument buffer.
// It exists to split one failure into two: if the probe finds hits and the
// megakernel does not, the structures are fine and the argument buffer is not.
static const char *kProbeSrc =
"#include <metal_stdlib>\n"
"#include <metal_raytracing>\n"
"using namespace metal;\n"
"using namespace metal::raytracing;\n"
"struct PR { bool accept [[accept_intersection]]; float distance [[distance]]; };\n"
"[[intersection(bounding_box, instancing)]] PR probe_isect(float3 o [[origin]], float3 d [[direction]],\n"
"    float tmin [[min_distance]], float tmax [[max_distance]]) {\n"
"  PR r; r.accept = true; r.distance = tmin + 1.0f; return r;\n"
"}\n"
"kernel void probe_blas(device uint* out [[buffer(0)]],\n"
"                  primitive_acceleration_structure as [[buffer(1)]],\n"
"                  intersection_function_table<> tbl [[buffer(2)]],\n"
"                  device const float* rays [[buffer(3)]],\n"
"                  uint tid [[thread_position_in_grid]]) {\n"
"  ray r;\n"
"  r.origin    = float3(rays[tid*6+0], rays[tid*6+1], rays[tid*6+2]);\n"
"  r.direction = float3(rays[tid*6+3], rays[tid*6+4], rays[tid*6+5]);\n"
"  r.min_distance = 1e-4f; r.max_distance = 1e6f;\n"
"  intersector<> it;\n"
"  auto res = it.intersect(r, as, tbl);\n"
"  out[tid] = (res.type == intersection_type::none) ? 0u : 1u;\n"
"}\n"
"kernel void probe(device uint* out [[buffer(0)]],\n"
"                  instance_acceleration_structure as [[buffer(1)]],\n"
"                  intersection_function_table<instancing> tbl [[buffer(2)]],\n"
"                  device const float* rays [[buffer(3)]],\n"
"                  uint tid [[thread_position_in_grid]]) {\n"
"  ray r;\n"
"  r.origin    = float3(rays[tid*6+0], rays[tid*6+1], rays[tid*6+2]);\n"
"  r.direction = float3(rays[tid*6+3], rays[tid*6+4], rays[tid*6+5]);\n"
"  r.min_distance = 1e-4f; r.max_distance = 1e6f;\n"
"  intersector<instancing> it;\n"
"  it.assume_geometry_type(geometry_type::bounding_box);\n"
"  auto res = it.intersect(r, as, 0xFFFFFFFF, tbl);\n"
"  out[tid] = (res.type == intersection_type::none) ? 0u : 1u;\n"
"}\n";

int mrt_probe(MRT *m, const float *rays, int n, uint32_t *hits, int use_blas, char *err, int errn) {
    @autoreleasepool {
        NSError *e = nil;
        MTLCompileOptions *co = [[MTLCompileOptions alloc] init];
        id<MTLLibrary> lib = [m->dev newLibraryWithSource:[NSString stringWithUTF8String:kProbeSrc]
                                                  options:co error:&e];
        if (!lib) { set_err(err, errn, [NSString stringWithFormat:@"probe compile: %@", e]); return 0; }
        id<MTLFunction> kf = [lib newFunctionWithName:(use_blas ? @"probe_blas" : @"probe")];
        id<MTLFunction> isf = [lib newFunctionWithName:@"probe_isect"];
        MTLLinkedFunctions *lf = [MTLLinkedFunctions linkedFunctions];
        lf.functions = @[isf];
        MTLComputePipelineDescriptor *pd = [[MTLComputePipelineDescriptor alloc] init];
        pd.computeFunction = kf;
        pd.linkedFunctions = lf;
        id<MTLComputePipelineState> ps = [m->dev newComputePipelineStateWithDescriptor:pd
                                             options:MTLPipelineOptionNone reflection:nil error:&e];
        if (!ps) { set_err(err, errn, [NSString stringWithFormat:@"probe pipeline: %@", e]); return 0; }

        MTLIntersectionFunctionTableDescriptor *td =
            [MTLIntersectionFunctionTableDescriptor intersectionFunctionTableDescriptor];
        td.functionCount = 1;
        id<MTLIntersectionFunctionTable> tbl = [ps newIntersectionFunctionTableWithDescriptor:td];
        [tbl setFunction:[ps functionHandleWithFunction:isf] atIndex:0];

        id<MTLBuffer> rb = [m->dev newBufferWithBytes:rays length:sizeof(float)*6*n
                                              options:MTLResourceStorageModeShared];
        id<MTLBuffer> ob = [m->dev newBufferWithLength:sizeof(uint32_t)*n
                                               options:MTLResourceStorageModeShared];
        id<MTLCommandBuffer> cb = [m->queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:ps];
        [enc setBuffer:ob offset:0 atIndex:0];
        [enc setAccelerationStructure:(use_blas ? m->geom_blas[0] : m->geom_tlas) atBufferIndex:1];
        [enc setIntersectionFunctionTable:tbl atBufferIndex:2];
        [enc setBuffer:rb offset:0 atIndex:3];
        [enc useResource:m->geom_tlas usage:MTLResourceUsageRead];
        for (int i = 0; i < m->n_geom; i++) {
            [enc useResource:m->geom_blas[i] usage:MTLResourceUsageRead];
            [enc useResource:m->geom_data[i] usage:MTLResourceUsageRead];
        }
        [enc dispatchThreads:MTLSizeMake(n,1,1) threadsPerThreadgroup:MTLSizeMake(n,1,1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
        if ([cb error]) { set_err(err, errn, [NSString stringWithFormat:@"probe: %@", [cb error]]); return 0; }
        memcpy(hits, [ob contents], sizeof(uint32_t)*n);
        return 1;
    }
}

// mrt_selftest builds a one-box BLAS and a one-instance identity TLAS from
// scratch and traces a ray that must hit both. It separates "our scene data is
// wrong" from "our use of the instancing API is wrong", which no amount of
// staring at the real structures can.
int mrt_selftest(MRT *m, uint32_t *blas_hit, uint32_t *tlas_hit, char *err, int errn) {
    @autoreleasepool {
        MTLAxisAlignedBoundingBox box;
        box.min = (MTLPackedFloat3){ -1, -1, -1 };
        box.max = (MTLPackedFloat3){  1,  1,  1 };
        id<MTLBuffer> bb = [m->dev newBufferWithBytes:&box length:sizeof(box)
                                              options:MTLResourceStorageModeShared];
        uint32_t zero = 0;
        id<MTLBuffer> pd = [m->dev newBufferWithBytes:&zero length:4
                                              options:MTLResourceStorageModeShared];
        MTLAccelerationStructureBoundingBoxGeometryDescriptor *g =
            [MTLAccelerationStructureBoundingBoxGeometryDescriptor descriptor];
        g.boundingBoxBuffer = bb;
        g.boundingBoxCount = 1;
        g.primitiveDataBuffer = pd;
        g.primitiveDataStride = 4;
        g.primitiveDataElementSize = 4;
        g.opaque = NO;
        MTLPrimitiveAccelerationStructureDescriptor *pdesc =
            [MTLPrimitiveAccelerationStructureDescriptor descriptor];
        pdesc.geometryDescriptors = @[g];
        id<MTLAccelerationStructure> blas = build_one(m, pdesc);
        if (!blas) { set_err(err, errn, @"selftest BLAS build failed"); return 0; }

        MTLAccelerationStructureInstanceDescriptor inst;
        memset(&inst, 0, sizeof(inst));
        inst.transformationMatrix.columns[0].elements[0] = 1;
        inst.transformationMatrix.columns[1].elements[1] = 1;
        inst.transformationMatrix.columns[2].elements[2] = 1;
        inst.options = MTLAccelerationStructureInstanceOptionNone;
        inst.mask = 0xFFFFFFFF;
        inst.intersectionFunctionTableOffset = 0;
        inst.accelerationStructureIndex = 0;
        id<MTLBuffer> ib = [m->dev newBufferWithBytes:&inst length:sizeof(inst)
                                              options:MTLResourceStorageModeShared];
        MTLInstanceAccelerationStructureDescriptor *idesc =
            [MTLInstanceAccelerationStructureDescriptor descriptor];
        idesc.instancedAccelerationStructures = @[blas];
        idesc.instanceCount = 1;
        idesc.instanceDescriptorBuffer = ib;
        id<MTLAccelerationStructure> tlas = build_one(m, idesc);
        if (!tlas) { set_err(err, errn, @"selftest TLAS build failed"); return 0; }

        // Swap the real structures out, reuse the probe, put them back.
        id<MTLAccelerationStructure> save_blas = m->geom_blas[0];
        id<MTLAccelerationStructure> save_tlas = m->geom_tlas;
        id<MTLBuffer> save_data = m->geom_data[0];
        int save_n = m->n_geom;
        m->geom_blas[0] = blas; m->geom_tlas = tlas; m->geom_data[0] = pd; m->n_geom = 1;

        float ray[6] = { -10, 0, 0, 1, 0, 0 }; // straight at the box
        int okb = mrt_probe(m, ray, 1, blas_hit, 1, err, errn);
        int okt = mrt_probe(m, ray, 1, tlas_hit, 0, err, errn);

        m->geom_blas[0] = save_blas; m->geom_tlas = save_tlas;
        m->geom_data[0] = save_data; m->n_geom = save_n;
        return okb && okt;
    }
}
