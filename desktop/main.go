package main

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var desktopAssets embed.FS

//go:embed all:nodeassets
var nodeAssets embed.FS

func main() {
	if handleMaintenanceCommand(os.Args) {
		return
	}
	discoveryAssets, err := fs.Sub(desktopAssets, "frontend")
	if err != nil {
		panic(err)
	}
	chatAssets, err := fs.Sub(nodeAssets, "nodeassets")
	if err != nil {
		panic(err)
	}

	app := NewApp(chatAssets)
	chatFiles := http.FileServer(http.FS(chatAssets))
	chatHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/chat/") {
			http.NotFound(response, request)
			return
		}
		http.StripPrefix("/chat", chatFiles).ServeHTTP(response, request)
	})
	appMenu := menu.NewMenu()
	nodeMenu := appMenu.AddSubmenu("节点")
	nodeMenu.AddText("发现节点", keys.CmdOrCtrl("D"), func(_ *menu.CallbackData) {
		app.ShowDiscovery()
	})
	nodeMenu.AddText("连接本机", keys.CmdOrCtrl("L"), func(_ *menu.CallbackData) {
		app.ConnectLocal()
	})
	nodeMenu.AddSeparator()
	nodeMenu.AddText("退出", keys.CmdOrCtrl("Q"), func(_ *menu.CallbackData) {
		app.Quit()
	})

	err = wails.Run(&options.App{
		Title:     "NodeCrypt Desktop",
		Width:     1180,
		Height:    780,
		MinWidth:  860,
		MinHeight: 600,
		Menu:      appMenu,
		AssetServer: &assetserver.Options{
			Assets:  discoveryAssets,
			Handler: chatHandler,
		},
		BackgroundColour: &options.RGBA{R: 243, G: 245, B: 247, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.nodecrypt.desktop",
		},
	})
	if err != nil {
		fmt.Println("NodeCrypt Desktop failed:", err)
	}
}
