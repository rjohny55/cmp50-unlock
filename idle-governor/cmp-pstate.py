#!/usr/bin/env python3
"""Force one NVIDIA GPU into a P-state through the private NVAPI entry point."""

import argparse
import ctypes
import re
import sys


NVAPI_OK = 0
NVAPI_MAX_PHYSICAL_GPUS = 64
NVAPI_ENUM_PHYSICAL_GPUS = 0xE5AC921F
NVAPI_GPU_GET_BUS_ID = 0x1BE0B8E5
NVAPI_GPU_SET_FORCE_PSTATE = 0x025BFB10
NVAPI_INITIALIZE = 0x0150E828
NVAPI_UNLOAD = 0xD22BDD7E


def status_text(status):
    return "0x%08x" % (ctypes.c_uint32(status).value,)


def bus_number(bus_id):
    match = re.search(r"(?:^|:)([0-9a-fA-F]{2}):[0-9a-fA-F]{2}\.[0-7]$",
                      bus_id.strip())
    if not match:
        raise ValueError("invalid PCI bus ID: %s" % bus_id)
    return int(match.group(1), 16)


def query_function(query, function_id, restype, *argtypes):
    address = query(ctypes.c_uint32(function_id))
    if not address:
        raise RuntimeError("NVAPI function 0x%08x is unavailable" % function_id)
    return ctypes.CFUNCTYPE(restype, *argtypes)(address)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bus-id", required=True,
                        help="Linux PCI bus ID, for example 00000000:01:00.0")
    parser.add_argument("--pstate", required=True, type=int,
                        help="P-state number: 0..15, or 16 for automatic/high")
    args = parser.parse_args()
    if not 0 <= args.pstate <= 16:
        parser.error("--pstate must be between 0 and 16")

    try:
        wanted_bus = bus_number(args.bus_id)
        api = ctypes.CDLL("libnvidia-api.so.1")
        query = api.nvapi_QueryInterface
        query.argtypes = [ctypes.c_uint32]
        query.restype = ctypes.c_void_p

        initialize = query_function(query, NVAPI_INITIALIZE, ctypes.c_int32)
        unload = query_function(query, NVAPI_UNLOAD, ctypes.c_int32)
        enum_gpus = query_function(
            query, NVAPI_ENUM_PHYSICAL_GPUS, ctypes.c_int32,
            ctypes.POINTER(ctypes.c_void_p), ctypes.POINTER(ctypes.c_uint32))
        get_bus_id = query_function(
            query, NVAPI_GPU_GET_BUS_ID, ctypes.c_int32,
            ctypes.c_void_p, ctypes.POINTER(ctypes.c_uint32))
        set_force_pstate = query_function(
            query, NVAPI_GPU_SET_FORCE_PSTATE, ctypes.c_int32,
            ctypes.c_void_p, ctypes.c_uint32, ctypes.c_uint32)
    except (OSError, RuntimeError, ValueError) as exc:
        print("cmp-pstate: %s" % exc, file=sys.stderr)
        return 1

    status = initialize()
    if status != NVAPI_OK:
        print("cmp-pstate: NvAPI_Initialize failed (%s)" % status_text(status),
              file=sys.stderr)
        return 1

    try:
        handles = (ctypes.c_void_p * NVAPI_MAX_PHYSICAL_GPUS)()
        count = ctypes.c_uint32()
        status = enum_gpus(handles, ctypes.byref(count))
        if status != NVAPI_OK:
            print("cmp-pstate: GPU enumeration failed (%s)" %
                  status_text(status), file=sys.stderr)
            return 1

        handle = None
        for index in range(min(count.value, NVAPI_MAX_PHYSICAL_GPUS)):
            bus = ctypes.c_uint32()
            status = get_bus_id(handles[index], ctypes.byref(bus))
            if status == NVAPI_OK and bus.value == wanted_bus:
                handle = handles[index]
                break
        if handle is None:
            print("cmp-pstate: NVAPI GPU for PCI bus %02x was not found" %
                  wanted_bus, file=sys.stderr)
            return 1

        status = set_force_pstate(handle, args.pstate, 0)
        if status != NVAPI_OK:
            print("cmp-pstate: P%d request failed (%s)" %
                  (args.pstate, status_text(status)), file=sys.stderr)
            return 1
        return 0
    finally:
        unload()


if __name__ == "__main__":
    sys.exit(main())
