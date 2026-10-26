#include <cuda_runtime.h>
#include <cstdio>
#include <cstdlib>
#include <cstdint>
#include <vector>

#define CU(call) do { cudaError_t e = (call); if (e != cudaSuccess) { \
  std::fprintf(stderr, "%s:%d %s: %s\n", __FILE__, __LINE__, #call, cudaGetErrorString(e)); std::exit(1); } } while (0)

__host__ __device__ uint32_t pattern(size_t i, uint32_t seed) {
  return uint32_t(i) * 1664525u + seed;
}
__global__ void fill(uint32_t *dst, size_t count, uint32_t seed) {
  for (size_t i = blockIdx.x * blockDim.x + threadIdx.x; i < count; i += size_t(gridDim.x) * blockDim.x)
    dst[i] = pattern(i, seed);
}
__global__ void remote_read(const uint32_t *peer, uint32_t *local, size_t count) {
  for (size_t i = blockIdx.x * blockDim.x + threadIdx.x; i < count; i += size_t(gridDim.x) * blockDim.x)
    local[i] = peer[i] ^ 0x55aa55aau;
}
__global__ void remote_write(uint32_t *peer, size_t count, uint32_t seed) {
  for (size_t i = blockIdx.x * blockDim.x + threadIdx.x; i < count; i += size_t(gridDim.x) * blockDim.x)
    peer[i] = pattern(i, seed);
}
void verify(uint32_t *ptr, size_t n, uint32_t seed, uint32_t mask, const char *phase) {
  std::vector<uint32_t> host(n);
  CU(cudaMemcpy(host.data(), ptr, n * sizeof(uint32_t), cudaMemcpyDeviceToHost));
  for (size_t i = 0; i < n; ++i) if (host[i] != (pattern(i, seed) ^ mask)) {
    std::fprintf(stderr, "%s CORRUPTION at %zu actual=%08x expected=%08x\n", phase, i, host[i], pattern(i, seed) ^ mask);
    std::exit(2);
  }
  std::printf("%s PASS (%zu bytes fully verified)\n", phase, n * sizeof(uint32_t));
  std::fflush(stdout);
}
int main(int argc, char **argv) {
  const size_t n = 16 * 1024 * 1024, bytes = n * sizeof(uint32_t);
  int count = 0; CU(cudaGetDeviceCount(&count));
  if (count != 2) return 3;
  bool capable = true;
  for (int src = 0; src < 2; ++src) {
    int access = 0; CU(cudaDeviceCanAccessPeer(&access, src, 1-src));
    std::printf("CUDA canAccessPeer %d -> %d: %d\n", src, 1-src, access);
    capable &= access != 0;
  }
  std::fflush(stdout);
  if (argc > 1) return capable ? 0 : 4;  // capability-only mode; no DMA
  if (!capable) return 4;
  for (int src = 0; src < 2; ++src) {
    const int dst = 1-src;
    uint32_t *a = nullptr, *b = nullptr;
    CU(cudaSetDevice(src)); CU(cudaDeviceEnablePeerAccess(dst, 0)); CU(cudaMalloc(&a, bytes));
    fill<<<4096,256>>>(a, n, 1013904223u); CU(cudaGetLastError()); CU(cudaDeviceSynchronize());
    CU(cudaSetDevice(dst)); CU(cudaDeviceEnablePeerAccess(src, 0)); CU(cudaMalloc(&b, bytes));
    remote_read<<<4096,256>>>(a, b, n); CU(cudaGetLastError()); CU(cudaDeviceSynchronize());
    verify(b, n, 1013904223u, 0x55aa55aau, "remote kernel READ");
    remote_write<<<4096,256>>>(a, n, 0xc001c0deu); CU(cudaGetLastError()); CU(cudaDeviceSynchronize());
    CU(cudaSetDevice(src)); verify(a, n, 0xc001c0deu, 0, "remote kernel WRITE");
    CU(cudaSetDevice(dst));
    cudaEvent_t start, end; CU(cudaEventCreate(&start)); CU(cudaEventCreate(&end));
    CU(cudaMemcpyPeerAsync(b, dst, a, src, bytes)); CU(cudaDeviceSynchronize());
    CU(cudaEventRecord(start));
    for (int repeat = 0; repeat < 32; ++repeat) CU(cudaMemcpyPeerAsync(b, dst, a, src, bytes));
    CU(cudaEventRecord(end)); CU(cudaEventSynchronize(end));
    float elapsed = 0; CU(cudaEventElapsedTime(&elapsed, start, end));
    verify(b, n, 0xc001c0deu, 0, "peer DMA COPY");
    std::printf("P2P %d -> %d: %.3f GB/s (32 x 64 MiB)\n", src, dst, double(bytes) * 32 / (elapsed * 1e6));
    CU(cudaEventDestroy(start)); CU(cudaEventDestroy(end)); CU(cudaFree(b)); CU(cudaDeviceDisablePeerAccess(src));
    CU(cudaSetDevice(src)); CU(cudaFree(a)); CU(cudaDeviceDisablePeerAccess(dst));
    std::fflush(stdout);
  }
  std::puts("P2P_BIDIRECTIONAL_DATA_INTEGRITY_PASS");
}
