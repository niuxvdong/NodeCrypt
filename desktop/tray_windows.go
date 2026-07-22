//go:build windows

package main

import (
	_ "embed"

	"github.com/getlantern/systray"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

func startDesktopTray(onOpen, onQuit func()) {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("NodeCrypt Desktop - 局域网节点运行中")
		openItem := systray.AddMenuItem("打开 NodeCrypt", "显示 NodeCrypt 窗口")
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("退出程序", "关闭 NodeCrypt 节点")
		go func() {
			for {
				select {
				case <-openItem.ClickedCh:
					onOpen()
				case <-quitItem.ClickedCh:
					onQuit()
					return
				}
			}
		}()
	}, func() {})
}

func stopDesktopTray() {
	systray.Quit()
}
