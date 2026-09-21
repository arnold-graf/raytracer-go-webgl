# mslbind

naga emits `[[user(fake0)]]`, `[[user(fake1)]]`, … for kernel buffer arguments
instead of real `[[buffer(N)]]` indices — placeholders that the WebGPU runtime
would normally fill in from the pipeline layout. Metal's offline compiler
rejects them, so anything that wants to compile naga's MSL directly (the
occupancy probe, the spill instrument, the Metal RT experiments) has to assign
indices first.

`mslbind in.metal out.metal` numbers them per kernel in declaration order. On
the current megakernel that is 29 buffers for `main_` and 30 for `aa_resolve`.

The kernel regex has to tolerate the attribute `--metal-version 3.0` prepends:

```go
reKernel = regexp.MustCompile(`(?m)^(?:\[\[max_total_threads_per_threadgroup\(\d+\)\]\] )?kernel void (\w+)\(`)
```

That same attribute must be *stripped* before taking an occupancy reading, since
it pins `maxTotalThreadsPerThreadgroup` to whatever naga wrote rather than
reporting what the shader actually affords.
