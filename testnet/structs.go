package main

import (
	"log"
	"os"
	"syscall"
	"time"
)

// ------------------------------------------------ 重启策略
type SystemRp struct {
	delay       time.Duration
	beforeFuncs []func()
}

func NewSystemRp() *SystemRp {
	self := &SystemRp{}
	self.beforeFuncs = make([]func(), 0, 10)
	self.delay = time.Hour * 12
	return self
}

func (self *SystemRp) SubscribeBeforeFunc(beforeFunc func()) {
	self.beforeFuncs = append(self.beforeFuncs, beforeFunc)
}

func (self *SystemRp) AutoRestartProcess() {
	time.AfterFunc(self.delay, self.RestartProcess)
}

func (self *SystemRp) RestartProcess() {
	if len(self.beforeFuncs) > 0 {
		for _, beforeFunc := range self.beforeFuncs {
			beforeFunc()
		}
	}
	args := append([]string{}, os.Args[:]...)
	execErr := syscall.Exec(args[0], args, os.Environ())
	if execErr != nil {
		log.Fatal(execErr)
	}
}
