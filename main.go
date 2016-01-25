package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/process"
	"github.com/spf13/cobra"
)

type ProcessStatus struct {
	Pid  int32
	Name string  // Name of process
	CPU  float64 // Percent of CPU used since last check
	VMS  uint64  // Virtual memory size
	RSS  uint64  // Resident set size
	Swap uint64  // Swap size
}

var (
	pidMap map[int32]*process.Process

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
	pidMap = make(map[int32]*process.Process)

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

	execCmd := exec.Command("rkt", "run", args[0], "--insecure-options=image", "--net=host")
	err = execCmd.Start()
	if err != nil {
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for _ = range c {
			err := killAllChildren(int32(execCmd.Process.Pid))
			if err != nil {
				fmt.Fprintf(os.Stderr, "cleanup failed: %v\n", err)
			}
			os.Exit(1)
		}
	}()

	usages := make(map[int32][]*ProcessStatus)

	timeToStop := time.Now().Add(d)
	for time.Now().Before(timeToStop) {
		usage, err := getUsage(int32(execCmd.Process.Pid))
		if err != nil {
			panic(err)
		}
		if flagVerbose {
			printUsage(usage)
		}

		for _, ps := range usage {
			usages[ps.Pid] = append(usages[ps.Pid], ps)
		}

		_, err = process.NewProcess(int32(execCmd.Process.Pid))
		if err != nil {
			// process.Process.IsRunning is not implemented yet
			fmt.Fprintf(os.Stderr, "rkt exited prematurely\n")
			break
		}

		time.Sleep(time.Second)
	}

	err = killAllChildren(int32(execCmd.Process.Pid))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup failed: %v\n", err)
	}

	for _, processHistory := range usages {
		var avgCPU float64
		var avgMem uint64
		var peakMem uint64

		for _, p := range processHistory {
			avgCPU += p.CPU
			avgMem += p.RSS
			if peakMem < p.RSS {
				peakMem = p.RSS
			}
		}

		avgCPU = avgCPU / float64(len(processHistory))
		avgMem = avgMem / uint64(len(processHistory))

		fmt.Printf("%s(%d): seconds alive: %d  avg CPU: %f%%  avg Mem: %s  peak Mem: %s\n", processHistory[0].Name, processHistory[0].Pid, len(processHistory), avgCPU, formatSize(avgMem), formatSize(peakMem))
	}
}

func killAllChildren(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	processes := []*process.Process{p}
	for i := 0; i < len(processes); i++ {
		children, err := processes[i].Children()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.Sys().(syscall.WaitStatus).ExitStatus() == 1 {
				// An ExitError with a code of 1 will be returned when there are no children
				continue
			} else {
				return err
			}
		}
		processes = append(processes, children...)
	}
	for _, p := range processes {
		osProcess, err := os.FindProcess(int(p.Pid))
		if err != nil {
			if err.Error() == "os: process already finished" {
				continue
			}
			return err
		}
		err = osProcess.Kill()
		if err != nil {
			return err
		}
	}
	return nil
}

func getUsage(pid int32) ([]*ProcessStatus, error) {
	var statuses []*ProcessStatus
	pids := []int32{pid}
	for i := 0; i < len(pids); i++ {
		proc, ok := pidMap[pids[i]]
		if !ok {
			var err error
			proc, err = process.NewProcess(pids[i])
			if err != nil {
				return nil, err
			}
			pidMap[pids[i]] = proc
		}
		s, err := getProcStatus(proc)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, s)

		children, err := proc.Children()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.Sys().(syscall.WaitStatus).ExitStatus() == 1 {
				// An ExitError with a code of 1 will be returned when there are no children
				continue
			} else {
				return nil, err
			}
		}

	childloop:
		for _, child := range children {
			for _, p := range pids {
				if p == child.Pid {
					fmt.Printf("%d is in %#v\n", p, pids)
					continue childloop
				}
			}
			pids = append(pids, child.Pid)
		}
	}
	return statuses, nil
}

func getProcStatus(p *process.Process) (*ProcessStatus, error) {
	n, err := p.Name()
	if err != nil {
		return nil, err
	}
	c, err := p.CPUPercent(0)
	if err != nil {
		return nil, err
	}
	m, err := p.MemoryInfo()
	if err != nil {
		return nil, err
	}
	return &ProcessStatus{
		Pid:  p.Pid,
		Name: n,
		CPU:  c,
		VMS:  m.VMS,
		RSS:  m.RSS,
		Swap: m.Swap,
	}, nil
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

func printUsage(statuses []*ProcessStatus) {
	for _, s := range statuses {
		fmt.Printf("%s(%d): Mem: %s CPU: %f\n", s.Name, s.Pid, formatSize(s.VMS), s.CPU)
	}
	fmt.Printf("\n")
}
