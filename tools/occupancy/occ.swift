// occ — report each compute kernel's occupancy ceiling.
//
// maxTotalThreadsPerThreadgroup is derived by the Metal compiler from the
// function's register use: the more registers a thread needs, the fewer threads
// the hardware can keep resident. It is the closest thing to a register count
// that is reachable without Xcode's GPU debugger, and unlike a frame capture it
// is a number a script can diff.
import Metal
import Foundation

let args = CommandLine.arguments
guard args.count > 1 else {
    FileHandle.standardError.write("usage: occ <file.metal> [label]\n".data(using: .utf8)!)
    exit(2)
}
let label = args.count > 2 ? args[2] : args[1]
guard let dev = MTLCreateSystemDefaultDevice() else { fatalError("no Metal device") }
let src = try String(contentsOfFile: args[1], encoding: .utf8)

let lib: MTLLibrary
do {
    lib = try dev.makeLibrary(source: src, options: MTLCompileOptions())
} catch {
    FileHandle.standardError.write("compile failed: \(error)\n".data(using: .utf8)!)
    exit(1)
}

func pad(_ s: String, _ n: Int) -> String {
    s.count >= n ? s : s + String(repeating: " ", count: n - s.count)
}

print("# \(label)   device=\(dev.name)")
print(pad("entry", 20) + pad("maxThreads/TG", 15) + pad("simdWidth", 11) + "tgMem")
for name in lib.functionNames.sorted() {
    guard let fn = lib.makeFunction(name: name) else { continue }
    do {
        let pso = try dev.makeComputePipelineState(function: fn)
        print(pad(name, 20)
            + pad(String(pso.maxTotalThreadsPerThreadgroup), 15)
            + pad(String(pso.threadExecutionWidth), 11)
            + String(pso.staticThreadgroupMemoryLength))
    } catch {
        print(pad(name, 20) + "pipeline failed: \(error)")
    }
}
