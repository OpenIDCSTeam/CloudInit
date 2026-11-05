package status

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// gpuSample 单块 GPU 的负载信息
type gpuSample struct {
	Index int
	Load  int
}

// readGPUs 通过 nvidia-smi 读取 GPU 数量与使用率（等价于 GPUtil.getGPUs）
func readGPUs() ([]gpuSample, error) {
	out, err := exec.Command(
		"nvidia-smi",
		"--query-gpu=index,utilization.gpu",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return nil, err
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, errors.New("未检测到GPU")
	}

	gpus := make([]gpuSample, 0, 4)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 {
			continue
		}
		index, errIndex := strconv.Atoi(strings.TrimSpace(fields[0]))
		load, errLoad := strconv.Atoi(strings.TrimSpace(fields[1]))
		if errIndex != nil || errLoad != nil {
			continue
		}
		gpus = append(gpus, gpuSample{Index: index, Load: load})
	}

	if len(gpus) == 0 {
		return nil, errors.New("未检测到GPU")
	}
	return gpus, nil
}
