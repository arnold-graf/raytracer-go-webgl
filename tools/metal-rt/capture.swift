// capture — writes a .gputrace of the real megakernel so Xcode can show its
// pipeline statistics: register use, occupancy and scratch.
//
// Scratch is the one resource that has repeatedly moved this renderer (the
// box_holed_nearest array was 14%, the traversal stacks 7%) and no Metal API
// exposes it. Xcode's Metal debugger does, but only from a frame capture, and a
// capture needs MTLCaptureManager in-process -- which wgpu does not expose. So
// this builds the pipelines from the same MSL naga generates and captures those.
//
// Nothing executes. Every buffer is zeroed, so params.width and params.height
// are 0 and main's first bounds check returns on every thread. The pipeline is
// still created and still appears in the capture, which is all Xcode needs, and
// no garbage data can wedge the GPU.
//
//   naga --metal-version 3.0 internal/webgpu/shaders/trace_linked.wgsl /tmp/t.metal
//   go run ./tools/metal-rt/mslbind /tmp/t.metal /tmp/bound.metal
//   swiftc -O tools/metal-rt/capture.swift -o /tmp/capture
//   MTL_CAPTURE_ENABLED=1 /tmp/capture /tmp/bound.metal /tmp/trace.gputrace
//
// Then: open /tmp/trace.gputrace, pick the dispatch, and read the pipeline
// statistics pane.
import Metal
import Foundation

let args = CommandLine.arguments
guard args.count > 2 else {
    FileHandle.standardError.write("usage: capture <bound.metal> <out.gputrace>\n".data(using: .utf8)!)
    exit(2)
}
let outURL = URL(fileURLWithPath: args[2])
try? FileManager.default.removeItem(at: outURL)

guard let dev = MTLCreateSystemDefaultDevice(), let q = dev.makeCommandQueue() else { fatalError("no device") }
let src = try String(contentsOfFile: args[1], encoding: .utf8)
let lib = try dev.makeLibrary(source: src, options: MTLCompileOptions())

// Every kernel, so the capture carries the whole frame's worth of pipelines.
let names = lib.functionNames.sorted()
var pipes: [(String, MTLComputePipelineState)] = []
for n in names {
    guard let fn = lib.makeFunction(name: n) else { continue }
    do {
        let p = try dev.makeComputePipelineState(function: fn)
        pipes.append((n, p))
        print(String(format: "%-20s maxThreads/TG=%4d  simdWidth=%d", (n as NSString).utf8String!,
                     p.maxTotalThreadsPerThreadgroup, p.threadExecutionWidth))
    } catch {
        print("\(n): pipeline failed: \(error)")
    }
}

// 32 zeroed buffers, more than the 30 any entry point takes.
let bufs = (0..<32).map { _ in dev.makeBuffer(length: 1 << 20, options: .storageModeShared)! }
for b in bufs { memset(b.contents(), 0, b.length) }

let cap = MTLCaptureManager.shared()
guard cap.supportsDestination(.gpuTraceDocument) else {
    FileHandle.standardError.write("gpuTraceDocument unsupported; is MTL_CAPTURE_ENABLED=1 set?\n".data(using: .utf8)!)
    exit(1)
}
let desc = MTLCaptureDescriptor()
desc.captureObject = dev
desc.destination = .gpuTraceDocument
desc.outputURL = outURL
try cap.startCapture(with: desc)

let cb = q.makeCommandBuffer()!
let enc = cb.makeComputeCommandEncoder()!
for (n, p) in pipes {
    enc.setComputePipelineState(p)
    for i in 0..<32 { enc.setBuffer(bufs[i], offset: 0, index: i) }
    enc.pushDebugGroup(n)
    // One threadgroup. With params zeroed every thread returns on entry.
    enc.dispatchThreadgroups(MTLSize(width: 1, height: 1, depth: 1),
                             threadsPerThreadgroup: MTLSize(width: 8, height: 8, depth: 1))
    enc.popDebugGroup()
}
enc.endEncoding()
cb.commit()
cb.waitUntilCompleted()
cap.stopCapture()
print("wrote \(outURL.path)")
