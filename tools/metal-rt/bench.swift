// bench — traversal throughput of Metal's intersector over the scene's AABBs.
//
// Answers the one question that gates docs/metal-backend.md: is Apple's
// traversal actually faster than ours, or only cheaper in registers? Compare the
// rays/s it prints against the megakernel's own primary-ray traversal, measured
// by gutting main() to nearest_hit and running gpuprof with -aa=false.
import Metal
import Foundation

struct Cam {
    var pos: (Float, Float, Float) = (0, 0, 0)
    var fwd: (Float, Float, Float) = (0, 0, 0)
    var right: (Float, Float, Float) = (0, 0, 0)
    var up: (Float, Float, Float) = (0, 0, 0)
    var aspect: Float = 1, fovScale: Float = 1
    var w: UInt32 = 0, h: UInt32 = 0
}

let args = CommandLine.arguments
guard args.count > 2 else {
    FileHandle.standardError.write("usage: bench <scene.bin> <bench.metal> [iters]\n".data(using: .utf8)!)
    exit(2)
}
let iters = args.count > 3 ? Int(args[3])! : 200
let data = try Data(contentsOf: URL(fileURLWithPath: args[0 + 1]))
let src = try String(contentsOfFile: args[2], encoding: .utf8)

// ---- parse the dump ----
var off = 0
func u32() -> UInt32 { defer { off += 4 }; return data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: off, as: UInt32.self) } }
func f32() -> Float { defer { off += 4 }; return data.withUnsafeBytes { $0.loadUnaligned(fromByteOffset: off, as: Float.self) } }
let nBoxes = Int(u32()), W = Int(u32()), H = Int(u32())
var cam = Cam()
cam.pos = (f32(), f32(), f32()); cam.fwd = (f32(), f32(), f32())
cam.right = (f32(), f32(), f32()); cam.up = (f32(), f32(), f32())
cam.aspect = f32(); cam.fovScale = f32()
cam.w = UInt32(W); cam.h = UInt32(H)

guard let dev = MTLCreateSystemDefaultDevice(), let q = dev.makeCommandQueue() else { fatalError("no device") }
print("device=\(dev.name)  boxes=\(nBoxes)  rays/frame=\(W * H)")

// ---- geometry ----
// MTLAxisAlignedBoundingBox is two packed float3 = 24 bytes, matching the dump.
let boxBytes = nBoxes * 24
let boxBuf = dev.makeBuffer(length: boxBytes, options: .storageModeShared)!
data.withUnsafeBytes { raw in
    memcpy(boxBuf.contents(), raw.baseAddress!.advanced(by: off), boxBytes)
}

let geo = MTLAccelerationStructureBoundingBoxGeometryDescriptor()
geo.boundingBoxBuffer = boxBuf
geo.boundingBoxCount = nBoxes
geo.intersectionFunctionTableOffset = 0
let asDesc = MTLPrimitiveAccelerationStructureDescriptor()
asDesc.geometryDescriptors = [geo]

let sizes = dev.accelerationStructureSizes(descriptor: asDesc)
let accel = dev.makeAccelerationStructure(size: sizes.accelerationStructureSize)!
let scratch = dev.makeBuffer(length: max(sizes.buildScratchBufferSize, 16), options: .storageModePrivate)!
do {
    let cb = q.makeCommandBuffer()!
    let enc = cb.makeAccelerationStructureCommandEncoder()!
    enc.build(accelerationStructure: accel, descriptor: asDesc, scratchBuffer: scratch, scratchBufferOffset: 0)
    enc.endEncoding()
    let t0 = CFAbsoluteTimeGetCurrent()
    cb.commit(); cb.waitUntilCompleted()
    print(String(format: "build: %.2f ms", (CFAbsoluteTimeGetCurrent() - t0) * 1000))
}

// ---- pipeline with a linked intersection function ----
let lib = try dev.makeLibrary(source: src, options: MTLCompileOptions())
let isectFn = lib.makeFunction(name: "prim_isect")!
let linked = MTLLinkedFunctions()
linked.functions = [isectFn]
let pdesc = MTLComputePipelineDescriptor()
pdesc.computeFunction = lib.makeFunction(name: "bench")!
pdesc.linkedFunctions = linked
let pipe = try dev.makeComputePipelineState(descriptor: pdesc, options: [], reflection: nil)
print("bench kernel maxThreads/TG = \(pipe.maxTotalThreadsPerThreadgroup)")

let tdesc = MTLIntersectionFunctionTableDescriptor()
tdesc.functionCount = 1
let table = pipe.makeIntersectionFunctionTable(descriptor: tdesc)!
table.setFunction(pipe.functionHandle(function: isectFn)!, index: 0)
table.setBuffer(boxBuf, offset: 0, index: 0)

let camBuf = dev.makeBuffer(bytes: &cam, length: MemoryLayout<Cam>.stride, options: .storageModeShared)!
let outBuf = dev.makeBuffer(length: W * H * 4, options: .storageModeShared)!

// ---- run ----
let tg = MTLSize(width: 8, height: 8, depth: 1)
let grid = MTLSize(width: W, height: H, depth: 1)
func run(_ n: Int) -> Double {
    let t0 = CFAbsoluteTimeGetCurrent()
    for _ in 0..<n {
        let cb = q.makeCommandBuffer()!
        let e = cb.makeComputeCommandEncoder()!
        e.setComputePipelineState(pipe)
        e.setAccelerationStructure(accel, bufferIndex: 0)
        e.setIntersectionFunctionTable(table, bufferIndex: 1)
        e.setBuffer(camBuf, offset: 0, index: 2)
        e.setBuffer(outBuf, offset: 0, index: 3)
        e.useResource(boxBuf, usage: .read)
        e.dispatchThreads(grid, threadsPerThreadgroup: tg)
        e.endEncoding()
        cb.commit()
        cb.waitUntilCompleted()
    }
    return (CFAbsoluteTimeGetCurrent() - t0) / Double(n)
}
_ = run(20)
var best = Double.infinity
for _ in 0..<3 { best = min(best, run(iters)) }
let rays = Double(W * H)
print(String(format: "trace: %.3f ms/frame   %.1f M rays/s", best * 1000, rays / best / 1e6))

// sanity: how many rays actually hit something
let px = outBuf.contents().bindMemory(to: Float.self, capacity: W * H)
var hits = 0
for i in 0..<(W * H) where px[i] >= 0 { hits += 1 }
print(String(format: "hit coverage: %.1f%%", 100.0 * Double(hits) / rays))
