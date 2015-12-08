package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagVerbose  bool
	flagDuration string

	cmdRktMonitor = &cobra.Command{
		Use:     "rkt-monitor IMAGE",
		Short:   "Runs the specified ACI with rkt, and monitors rkt's usage",
		Example: "rkt-monitor mem-stresser.aci -v -d 30s",
		Run:     runRktMonitor,
	}
)

func init() {
	cmdRktMonitor.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Print current usage every second")
	cmdRktMonitor.Flags().StringVarP(&flagDuration, "duration", "d", "10s", "How long to run the ACI")
}

func main() {
	cmdRktMonitor.Execute()
}

func runRktMonitor(cmd *cobra.Command, args []string) {
	if len(args) != 1 {
		cmd.Usage()
		os.Exit(1)
	}

	d, err := time.ParseDuration(flagDuration)
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	execCmd := exec.Command("rkt", "run", args[0], "--insecure-options=image")
	err = execCmd.Start()
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for _ = range c {
			execCmd.Process.Kill()
			os.Exit(1)
		}
	}()

	usages := make(map[int][]ProcessStatus)

	var usage []ProcessStatus

	timeToEnd := time.Now().Add(d)
	for time.Now().Before(timeToEnd) {
		usage = getUsage(execCmd.Process.Pid, usage)
		if flagVerbose {
			printUsage(usage)
		}

		for _, process := range usage {
			usages[process.Pid] = append(usages[process.Pid], process)
		}

		time.Sleep(time.Second)
	}

	execCmd.Process.Kill()

	for _, processHistory := range usages {
		var avgCPU uint64
		var avgMem uint64
		var peakMem uint64

		for _, process := range processHistory {
			avgCPU += process.GetCPUUsageUser()
			avgMem += process.VmSize
			if peakMem < process.VmPeak {
				peakMem = process.VmPeak
			}
		}

		avgCPU = avgCPU / uint64(len(processHistory))
		avgMem = avgMem / uint64(len(processHistory))

		fmt.Printf("%s(%d): seconds alive: %d  avg CPU: %d%%  avg Mem: %s  peak Mem: %s\n", processHistory[0].Name, processHistory[0].Pid, len(processHistory), avgCPU, formatSize(avgMem), formatSize(peakMem))
	}
}

func getUsage(pid int, lastStatuses []ProcessStatus) []ProcessStatus {
	status := getProcStatus(pid, lastStatuses)
	children := getChildrenPids(pid)
	var childrenStatuses []ProcessStatus
	for _, child := range children {
		childrenStatuses = append(childrenStatuses, getUsage(child, lastStatuses)...)
	}
	return append([]ProcessStatus{status}, childrenStatuses...)
}

func formatSize(size uint64) string {
	if size > 1024*1024*1024 {
		return strconv.FormatUint(size/(1024*1024*1024), 10) + " gB"
	}
	if size > 1024*1024 {
		return strconv.FormatUint(size/(1024*1024), 10) + " mB"
	}
	if size > 1024 {
		return strconv.FormatUint(size/1024, 10) + " kB"
	}
	return strconv.FormatUint(size, 10) + " B"
}

func printUsage(statuses []ProcessStatus) {
	for _, status := range statuses {
		fmt.Printf("Pid: %s Name: %s CPU: %s FDSize: %s VmPeak: %s VmSize: %s VmHWM: %s VmRSS: %s Threads: %s\n",
			pad(strconv.Itoa(status.Pid)),
			pad(status.Name),
			pad(strconv.FormatUint(status.GetCPUUsageUser(), 10)+"%"),
			pad(formatSize(status.FDSize)),
			pad(formatSize(status.VmPeak)),
			pad(formatSize(status.VmSize)),
			pad(formatSize(status.VmHWM)),
			pad(formatSize(status.VmRSS)),
			pad(strconv.FormatUint(status.Threads, 10)))
	}
	fmt.Printf("\n")
}

type ProcessStatus struct {
	Pid           int
	Name          string
	FDSize        uint64 // Number of file descriptor slots currently allocated.
	VmPeak        uint64 // Peak virtual memory size.
	VmSize        uint64 // Virtual memory size
	VmHWM         uint64 // Peak resident set size ("high water mark").
	VmRSS         uint64 // Resident set size.
	Threads       uint64 // Number of threads in process containing this thread.
	Utime         uint64 // Userspace CPU ticks
	LastUtime     uint64 // Userspace CPU ticks last time we checked
	Stime         uint64 // Kernel CPU ticks
	LastStime     uint64 // Kernel CPU ticks last time we checked
	TimeTotal     uint64 // total system ticks
	LastTimeTotal uint64 // total system ticks last time we checked
}

// http://stackoverflow.com/questions/1420426/calculating-cpu-usage-of-a-process-in-linux
func (ps ProcessStatus) GetCPUUsageUser() uint64 {
	return 100 * (ps.Utime - ps.LastUtime) / (ps.TimeTotal - ps.LastTimeTotal)
}

func (ps ProcessStatus) GetCPUUsageKernel() uint64 {
	return 100 * (ps.Stime - ps.LastStime) / (ps.TimeTotal - ps.LastTimeTotal)
}

func pad(str string) string {
	for i := len(str); i < 16; i++ {
		str = str + " "
	}
	return str
}

func getProcStatus(pid int, lastStatuses []ProcessStatus) ProcessStatus {
	status := ProcessStatus{
		Pid: pid,
	}
	var lastStatus ProcessStatus
	for _, s := range lastStatuses {
		if s.Pid == pid {
			lastStatus = s
		}
	}
	status.LastUtime = lastStatus.Utime
	status.LastStime = lastStatus.Stime
	status.LastTimeTotal = lastStatus.TimeTotal

	blob, err := ioutil.ReadFile(path.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		panic(err)
	}
	lines := strings.Split(string(blob), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		tokens := strings.Split(line, ":")
		if len(tokens) != 2 {
			panic(fmt.Sprintf("couldn't parse: %q", line))
		}

		for i := 0; i < len(tokens); i++ {
			tokens[i] = strings.TrimSpace(tokens[i])
		}

		var err error
		switch tokens[0] {
		case "Name":
			status.Name = tokens[1]
		case "FDSize":
			status.FDSize, err = strconv.ParseUint(tokens[1], 10, 64)
			if err != nil {
				panic(err)
			}
		case "VmPeak":
			status.VmPeak = parseLabeledSize(tokens[1])
		case "VmSize":
			status.VmSize = parseLabeledSize(tokens[1])
		case "VmHWM":
			status.VmHWM = parseLabeledSize(tokens[1])
		case "VmRSS":
			status.VmRSS = parseLabeledSize(tokens[1])
		case "Threads":
			status.Threads, err = strconv.ParseUint(tokens[1], 10, 64)
			if err != nil {
				panic(err)
			}
		}
	}

	blob, err = ioutil.ReadFile("/proc/stat")
	if err != nil {
		panic(err)
	}
	lines = strings.Split(string(blob), "\n")
	for _, line := range lines {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		tokens := strings.Split(line, "	")
		for _, token := range tokens {
			for _, token := range strings.Split(token, " ") {
				num, err := strconv.ParseUint(token, 10, 64)
				if err != nil {
					continue
				}
				status.TimeTotal += num
			}
		}
	}
	if status.TimeTotal == 0 {
		panic("failed to read total time")
	}

	blob, err = ioutil.ReadFile(path.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		panic(err)
	}
	tokens := strings.Split(string(blob), " ")
	if len(tokens) < 15 {
		panic(fmt.Sprintf("couldn't parse: %q", string(blob)))
	}
	status.Utime, err = strconv.ParseUint(tokens[13], 10, 64)
	if err != nil {
		panic(err)
	}
	status.Stime, err = strconv.ParseUint(tokens[14], 10, 64)
	if err != nil {
		panic(err)
	}
	return status
}

func parseProcStat(blob string) (int, int, int) {
	return 0, 0, 0
}

func getChildrenPids(pid int) []int {
	blob, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if len(blob) == 0 {
		return nil
	}
	if err != nil {
		panic(err)
	}

	pidStrings := strings.Split(string(blob), "\n")

	var pids []int
	for _, str := range pidStrings {
		if str == "" {
			continue
		}
		childPid, err := strconv.Atoi(str)
		if err != nil {
			panic(err)
		}
		pids = append(pids, childPid)
	}

	return pids
}

func parseLabeledSize(size string) uint64 {
	var num uint64

	i, _ := fmt.Sscanf(size, "%d B", &num)
	if i == 1 {
		return num
	}

	i, _ = fmt.Sscanf(size, "%d kB", &num)
	if i == 1 {
		return num * 1024
	}

	i, _ = fmt.Sscanf(size, "%d mB", &num)
	if i == 1 {
		return num * 1024 * 1024
	}

	i, _ = fmt.Sscanf(size, "%d gB", &num)
	if i == 1 {
		return num * 1024 * 1024 * 1024
	}
	panic(fmt.Sprintf("unrecognized size label: %s", size))
}
