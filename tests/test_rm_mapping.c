// No GPU access: emulate the public GET ioctls and object allocations.
#define ioctl mock_ioctl
#define main cmp50_probe_main
#include "../verify/rm_issue_rate.c"
#undef main
#undef ioctl
#include <assert.h>
#include <stdarg.h>

static int failure;

int mock_ioctl(int fd, unsigned long request, ...)
{
    va_list args;
    void *data;
    (void)fd;
    va_start(args, request);
    data = va_arg(args, void *);
    va_end(args);
    if (_IOC_NR(request) == NV_ESC_CARD_INFO) {
        nv_ioctl_card_info_t *cards = data;
        if (failure == 1) {
            errno = EIO;
            return -1;
        }
        cards[0].valid = 1;
        cards[0].minor_number = 2;
        cards[0].gpu_id = 0x12345678;
        cards[0].pci_info.bus = 4;
        return 0;
    }
    if (_IOC_NR(request) == NV_ESC_RM_CONTROL) {
        NVOS54_PARAMETERS *control = data;
        NV0000_CTRL_GPU_GET_ID_INFO_V2_PARAMS *info = control->params;
        assert(control->cmd == NV0000_CTRL_CMD_GPU_GET_ID_INFO_V2);
        assert(info->gpuId == 0x12345678);
        control->status = failure == 2 ? 0x22 : 0;
        info->deviceInstance = 7;  // Deliberately different from minor 2.
        info->subDeviceInstance = 3;
        return 0;
    }
    if (_IOC_NR(request) == NV_ESC_RM_ALLOC) {
        NVOS64_PARAMETERS *allocation = data;
        if (allocation->hClass == NV01_DEVICE_0) {
            NV0080_ALLOC_PARAMETERS *params = allocation->pAllocParms;
            assert(params->deviceId == 7);
        } else {
            NV2080_ALLOC_PARAMETERS *params = allocation->pAllocParms;
            assert(allocation->hClass == NV20_SUBDEVICE_0);
            assert(params->subDeviceId == 3);
        }
        allocation->hObjectNew = 10;
        return 0;
    }
    assert(!"Unexpected ioctl in the read-only mapping test");
    return -1;
}

int main(void)
{
    NvU32 device = 0, subdevice = 0;
    NvHandle object;
    assert(resolve_device_instances(-1, 1, 2, &device, &subdevice));
    assert(device == 7 && subdevice == 3);
    assert(alloc_device(-1, 1, (int)device, &object));
    assert(alloc_subdevice(-1, 1, object, subdevice, &object));
    assert(!resolve_device_instances(-1, 1, 1, &device, &subdevice));
    failure = 1;
    assert(!resolve_device_instances(-1, 1, 2, &device, &subdevice));
    failure = 2;
    assert(!resolve_device_instances(-1, 1, 2, &device, &subdevice));
    puts("PASS_CMP50_RM_MAPPING_TESTS");
    return 0;
}
