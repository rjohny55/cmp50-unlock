"""NCCL all-reduce correctness; run only after real CUDA P2P passes."""
import datetime
import json
import time
import torch
import torch.distributed as dist
import torch.multiprocessing as mp


def worker(rank):
    torch.cuda.set_device(rank)
    dist.init_process_group("nccl", init_method="tcp://127.0.0.1:29679", rank=rank, world_size=2,
                            timeout=datetime.timedelta(seconds=90))
    try:
        for count in [1, 1024, 1048576, 16777216]:
            value = torch.empty(count, device=f"cuda:{rank}", dtype=torch.float32)
            elapsed = []
            for iteration in range(6):
                value.fill_(rank + 1)
                torch.cuda.synchronize()
                started = time.perf_counter()
                dist.all_reduce(value)
                torch.cuda.synchronize()
                elapsed.append(time.perf_counter() - started)
                assert torch.all(value == 3).item(), f"NCCL corruption rank={rank}, count={count}"
            if rank == 0:
                print(json.dumps({"nccl_allreduce": "PASS", "bytes": count * 4,
                                  "mean_ms_after_warmup": sum(elapsed[2:]) * 1000 / len(elapsed[2:])}), flush=True)
            del value
        dist.barrier()
    finally:
        dist.destroy_process_group()


if __name__ == "__main__":
    assert torch.cuda.device_count() == 2
    assert torch.cuda.can_device_access_peer(0, 1)
    assert torch.cuda.can_device_access_peer(1, 0)
    mp.spawn(worker, nprocs=2, join=True)
    print("NCCL_P2P_ALLREDUCE_INTEGRITY_PASS", flush=True)
