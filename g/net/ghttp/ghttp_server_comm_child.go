// Copyright 2017 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.
// Web Server进程间通信 - 子进程

package ghttp

import (
    "os"
    "fmt"
    "time"
    "strings"
    "runtime"
    "gitee.com/johng/gf/g/os/glog"
    "gitee.com/johng/gf/g/os/gproc"
    "gitee.com/johng/gf/g/os/gtime"
    "gitee.com/johng/gf/g/util/gconv"
    "gitee.com/johng/gf/g/encoding/gjson"
)

const (
    gPROC_CHILD_MAX_IDLE_TIME = 10000 // 子进程闲置时间(未开启心跳机制的时间)
)

// 心跳处理(方法为空，逻辑放到公共通信switch中进行处理)
func onCommChildHeartbeat(pid int, data []byte) {

}

// 平滑重启，子进程收到重启消息，那么将自身的ServerFdMap信息收集后发送给主进程，由主进程进行统一调度
func onCommChildReload(pid int, data []byte) {
    var buffer []byte = nil
    p := procManager.NewProcess(os.Args[0], os.Args, os.Environ())
    // windows系统无法进行文件描述符操作，只能重启进程
    if runtime.GOOS == "windows" {
        // windows下使用shutdown会造成协程阻塞，这里直接使用close强制关闭
        closeWebServers()
    } else {
        // 创建新的服务进程，子进程自动从父进程复制文件描述来监听同样的端口
        sfm := getServerFdMap()
        // 将sfm中的fd按照子进程创建时的文件描述符顺序进行整理，以便子进程获取到正确的fd
        for name, m := range sfm {
            for fdk, fdv := range m {
                if len(fdv) > 0 {
                    s := ""
                    for _, item := range strings.Split(fdv, ",") {
                        array := strings.Split(item, "#")
                        fd    := uintptr(gconv.Uint(array[1]))
                        if fd > 0 {
                            s += fmt.Sprintf("%s#%d,", array[0], 3 + len(p.ExtraFiles))
                            p.ExtraFiles = append(p.ExtraFiles, os.NewFile(fd, ""))
                        } else {
                            s += fmt.Sprintf("%s#%d,", array[0], 0)
                        }
                    }
                    sfm[name][fdk] = strings.TrimRight(s, ",")
                }
            }
        }
        buffer, _ = gjson.Encode(sfm)
    }
    p.PPid = gproc.PPid()
    if newPid, err := p.Start(); err == nil {
        sendProcessMsg(newPid, gMSG_START, buffer)
    } else {
        glog.Errorfln("%d: fork process failed, error:%s, %s", gproc.Pid(), err.Error(), string(buffer))
    }
}

// 完整重启
func onCommChildRestart(pid int, data []byte) {
    sendProcessMsg(gproc.PPid(), gMSG_RESTART, nil)
}

// 优雅关闭服务链接并退出
func onCommChildShutdown(pid int, data []byte) {
    if runtime.GOOS != "windows" {
        shutdownWebServers()
    }
    os.Exit(0)
}

// 强制性关闭服务链接并退出
func onCommChildClose(pid int, data []byte) {
    closeWebServers()
    os.Exit(0)
}

// 主进程与子进程相互异步方式发送心跳信息，保持活跃状态
func handleChildProcessHeartbeat() {
    for {
        time.Sleep(gPROC_HEARTBEAT_INTERVAL*time.Millisecond)
        sendProcessMsg(gproc.PPid(), gMSG_HEARTBEAT, nil)
        // 超过时间没有接收到主进程心跳，自动关闭退出
        if checkHeartbeat.Val() && (int(gtime.Millisecond()) - lastUpdateTime.Val() > gPROC_HEARTBEAT_TIMEOUT) {
            // 子进程有时会无法退出(僵尸?)，这里直接使用exit，而不是return
            glog.Printfln("%d: %d - %d > %d", gproc.Pid(), int(gtime.Millisecond()), lastUpdateTime.Val(), gPROC_HEARTBEAT_TIMEOUT)
            glog.Printfln("%d: heartbeat timeout[%dms], exit", gproc.Pid(), gPROC_HEARTBEAT_TIMEOUT)
            os.Exit(0)
        }
        // 未开启心跳检测的闲置超过一定时间则主动关闭
        if !checkHeartbeat.Val() && gproc.Uptime() > gPROC_CHILD_MAX_IDLE_TIME {
            glog.Printfln("%d: idle timeout[%dms], exit", gproc.Pid(), gPROC_CHILD_MAX_IDLE_TIME)
            os.Exit(0)
        }
    }
}