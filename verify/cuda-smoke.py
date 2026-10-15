#!/usr/bin/env python3
"""Small per-card CUDA correctness test; requires libcuda, not a CUDA toolkit.

Only the two board identities supported by the driver patches are tested.
This is an installation smoke test, not a performance or full VRAM stress test.
"""
import array
import ctypes as C
from pathlib import Path


PTX = b"""
.version 6.4
.target sm_75
.address_size 64
.visible .entry vector_add(
    .param .u64 a, .param .u64 b, .param .u64 out, .param .u32 count
) {
    .reg .pred p;
    .reg .b32 r<5>;
    .reg .b64 d<8>;
    .reg .f32 f<4>;
    ld.param.u64 d1, [a];
    ld.param.u64 d2, [b];
    ld.param.u64 d3, [out];
    ld.param.u32 r1, [count];
    mov.u32 r2, %ctaid.x;
    mov.u32 r3, %ntid.x;
    mov.u32 r4, %tid.x;
    mad.lo.s32 r2, r2, r3, r4;
    setp.ge.u32 p, r2, r1;
    @p bra done;
    mul.wide.u32 d4, r2, 4;
    add.s64 d5, d1, d4;
    add.s64 d6, d2, d4;
    add.s64 d7, d3, d4;
    ld.global.f32 f1, [d5];
    ld.global.f32 f2, [d6];
    add.f32 f3, f1, f2;
    st.global.f32 [d7], f3;
done:
    ret;
}
"""


def main():
    cuda = C.CDLL("libcuda.so.1")

    def api(name, *types):
        fn = getattr(cuda, name)
        fn.argtypes, fn.restype = list(types), C.c_int
        return fn

    ptr = C.c_void_p
    device_ptr = C.c_uint64
    init = api("cuInit", C.c_uint)
    device_count = api("cuDeviceGetCount", C.POINTER(C.c_int))
    pci_id = api("cuDeviceGetPCIBusId", C.c_char_p, C.c_int, C.c_int)
    ctx_create = api("cuCtxCreate_v2", C.POINTER(ptr), C.c_uint, C.c_int)
    ctx_destroy = api("cuCtxDestroy_v2", ptr)
    module_load = api("cuModuleLoadData", C.POINTER(ptr), ptr)
    module_unload = api("cuModuleUnload", ptr)
    get_function = api("cuModuleGetFunction", C.POINTER(ptr), ptr, C.c_char_p)
    alloc = api("cuMemAlloc_v2", C.POINTER(device_ptr), C.c_size_t)
    free = api("cuMemFree_v2", device_ptr)
    copy_htod = api("cuMemcpyHtoD_v2", device_ptr, ptr, C.c_size_t)
    copy_dtoh = api("cuMemcpyDtoH_v2", ptr, device_ptr, C.c_size_t)
    launch = api("cuLaunchKernel", ptr, C.c_uint, C.c_uint, C.c_uint,
                 C.c_uint, C.c_uint, C.c_uint, C.c_uint, ptr,
                 C.POINTER(ptr), C.POINTER(ptr))
    synchronize = api("cuCtxSynchronize")
    error_name = api("cuGetErrorName", C.c_int, C.POINTER(C.c_char_p))

    def check(result, operation):
        if result:
            name = C.c_char_p()
            error_name(result, C.byref(name))
            raise RuntimeError(f"{operation}: CUDA {result} {name.value!r}")

    check(init(0), "cuInit")
    count = C.c_int()
    check(device_count(C.byref(count)), "cuDeviceGetCount")
    tested = 0
    for ordinal in range(count.value):
        bus = C.create_string_buffer(32)
        check(pci_id(bus, len(bus), ordinal), "cuDeviceGetPCIBusId")
        bdf = bus.value.decode().lower()
        sysfs = Path("/sys/bus/pci/devices") / bdf
        ids = tuple(int((sysfs / field).read_text().strip(), 16) for field in
                    ("vendor", "device", "subsystem_vendor", "subsystem_device"))
        if ids[:2] != (0x10de, 0x1e09):
            continue
        if ids[2:] not in ((0x10de, 0x1554), (0x1462, 0x371f)):
            raise RuntimeError(f"Unsupported CMP50 subsystem: {bdf} {ids[2:]}")
        context, module, function = ptr(), ptr(), ptr()
        allocations = []
        try:
            check(ctx_create(C.byref(context), 0, ordinal), "cuCtxCreate")
            ptx = C.create_string_buffer(PTX)
            check(module_load(C.byref(module), ptx), "cuModuleLoadData")
            check(get_function(C.byref(function), module, b"vector_add"), "cuModuleGetFunction")
            size = 262144
            a = array.array("f", (float(i % 127) for i in range(size)))
            b = array.array("f", (float(i % 61) for i in range(size)))
            out = array.array("f", [0.0]) * size
            nbytes = size * a.itemsize
            for _ in range(3):
                address = device_ptr()
                check(alloc(C.byref(address), nbytes), "cuMemAlloc")
                allocations.append(address)
            check(copy_htod(allocations[0], a.buffer_info()[0], nbytes), "copy a")
            check(copy_htod(allocations[1], b.buffer_info()[0], nbytes), "copy b")
            length = C.c_uint(size)
            arguments = (ptr * 4)(*(C.cast(C.byref(value), ptr)
                                    for value in (*allocations, length)))
            check(launch(function, (size + 255) // 256, 1, 1, 256, 1, 1,
                         0, None, arguments, None), "cuLaunchKernel")
            check(synchronize(), "cuCtxSynchronize")
            check(copy_dtoh(out.buffer_info()[0], allocations[2], nbytes), "copy result")
            if any(x + y != z for x, y, z in zip(a, b, out)):
                raise RuntimeError(f"Vector-add correctness failed on {bdf}")
            print(f"PASS_CMP50_CUDA: ordinal={ordinal} pci={bdf} elements={size}", flush=True)
            tested += 1
        finally:
            for address in reversed(allocations):
                check(free(address), "cuMemFree")
            if module.value:
                check(module_unload(module), "cuModuleUnload")
            if context.value:
                check(ctx_destroy(context), "cuCtxDestroy")
    if not tested:
        raise SystemExit("No supported CMP50 GPUs visible to CUDA")
    print(f"PASS_CMP50_CUDA_ALL: {tested} card(s)")


if __name__ == "__main__":
    main()
