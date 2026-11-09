set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

export GOPROXY := "https://goproxy.cn,direct"

default: build

build:
    New-Item -ItemType Directory -Force dist | Out-Null
    go build -trimpath -tags mygo_noinspector -ldflags "-s -w -X github.com/egoist/mygo.production=1" -o dist/sphvideo.exe .

dev:
    go run .

tidy:
    go mod tidy

vet:
    go vet .

installer:
    go tool mygo build -platform windows/amd64

dist: build
    go tool mygo build -platform windows/amd64
    $setup = Get-ChildItem build/windows-amd64/*setup*.exe | Select-Object -First 1; Copy-Item $setup.FullName 'dist/视频号视频下载-安装包.exe' -Force
    Write-Output 'dist/sphvideo.exe'
    Write-Output 'dist/视频号视频下载-安装包.exe'

clean:
    Remove-Item -Recurse -Force dist,build -ErrorAction SilentlyContinue
