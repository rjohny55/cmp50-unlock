#define _POSIX_C_SOURCE 200809L

#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#define BAR0_MAP_SIZE 0x90000UL
#define PL_LINK_RATE_OFFSET 0x8c1c0UL
#define PL_LINK_RATE_POLICY_MASK 0x00160000U
#define PL_LINK_RATE_GEN2 0x00040000U

static unsigned long read_sysfs_hex(const char *device_dir, const char *name)
{
    char path[512];
    char value[64];
    FILE *file;
    char *end;
    unsigned long result;

    if (snprintf(path, sizeof(path), "%s/%s", device_dir, name) >=
        (int)sizeof(path))
        exit(2);
    file = fopen(path, "r");
    if (file == NULL || fgets(value, sizeof(value), file) == NULL) {
        fprintf(stderr, "cannot read %s: %s\n", path, strerror(errno));
        if (file != NULL)
            fclose(file);
        exit(2);
    }
    fclose(file);
    errno = 0;
    result = strtoul(value, &end, 0);
    if (errno != 0 || end == value)
        exit(2);
    return result;
}

int main(int argc, char **argv)
{
    char device_dir[512];
    char resource_path[544];
    unsigned long vendor, device, subsystem_vendor, subsystem_device;
    volatile uint32_t *pl_link_rate;
    uint32_t before, desired, after;
    void *bar0;
    int fd;

    if (argc != 3 || strcmp(argv[2], "--apply") != 0) {
        fprintf(stderr, "usage: %s DOMAIN:BUS:DEVICE.FUNCTION --apply\n",
                argv[0]);
        return 2;
    }
    if (snprintf(device_dir, sizeof(device_dir),
                 "/sys/bus/pci/devices/%s", argv[1]) >=
        (int)sizeof(device_dir))
        return 2;

    vendor = read_sysfs_hex(device_dir, "vendor");
    device = read_sysfs_hex(device_dir, "device");
    subsystem_vendor = read_sysfs_hex(device_dir, "subsystem_vendor");
    subsystem_device = read_sysfs_hex(device_dir, "subsystem_device");
    if (vendor != 0x10deUL || device != 0x1e09UL ||
        !((subsystem_vendor == 0x10deUL && subsystem_device == 0x1554UL) ||
          (subsystem_vendor == 0x1462UL && subsystem_device == 0x371fUL))) {
        fprintf(stderr, "refusing unsupported PCI device\n");
        return 3;
    }
    if (snprintf(resource_path, sizeof(resource_path), "%s/resource0",
                 device_dir) >= (int)sizeof(resource_path))
        return 2;

    fd = open(resource_path, O_RDWR | O_SYNC);
    if (fd < 0) {
        fprintf(stderr, "cannot open %s: %s\n", resource_path,
                strerror(errno));
        return 4;
    }
    bar0 = mmap(NULL, BAR0_MAP_SIZE, PROT_READ | PROT_WRITE, MAP_SHARED,
                fd, 0);
    if (bar0 == MAP_FAILED) {
        fprintf(stderr, "cannot map %s: %s\n", resource_path,
                strerror(errno));
        close(fd);
        return 4;
    }

    pl_link_rate = (volatile uint32_t *)
        ((unsigned char *)bar0 + PL_LINK_RATE_OFFSET);
    before = *pl_link_rate;
    if (before == UINT32_MAX) {
        fprintf(stderr, "%s BAR0 is not responding; refusing write\n",
                argv[1]);
        munmap(bar0, BAR0_MAP_SIZE);
        close(fd);
        return 5;
    }
    desired = (before & ~PL_LINK_RATE_POLICY_MASK) | PL_LINK_RATE_GEN2;
    __sync_synchronize();
    *pl_link_rate = desired;
    __sync_synchronize();
    after = *pl_link_rate;
    printf("%s PL_LINK_RATE before=0x%08" PRIx32
           " desired=0x%08" PRIx32 " after=0x%08" PRIx32 "\n",
           argv[1], before, desired, after);

    munmap(bar0, BAR0_MAP_SIZE);
    close(fd);
    return after == desired ? 0 : 5;
}
