set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

export GOPROXY := "https://goproxy.cn,direct"
export GOFLAGS := "-tags=embed_inject,embed_frontend_inject,sqlite_only"

default: build

build:
    New-Item -ItemType Directory -Force dist | Out-Null
    go tool mygo build -platform windows/amd64 -skip-installer
    Copy-Item build/windows-amd64/sphvideo.exe dist/sphvideo.exe -Force

dev:
    go run ./cmd/sphvideo

tidy:
    go mod tidy

vet:
    go vet -unsafeptr=false ./...

installer:
    go tool mygo build -platform windows/amd64

dist:
    New-Item -ItemType Directory -Force dist | Out-Null
    go tool mygo build -platform windows/amd64,linux/amd64,linux/arm64,darwin/arm64,darwin/amd64 -skip-dmg -skip-notarize
    Copy-Item build/windows-amd64/sphvideo.exe dist/sphvideo.exe -Force
    $setup = Get-ChildItem build/windows-amd64/*setup*.exe | Select-Object -First 1; Copy-Item $setup.FullName 'dist/视频号视频下载-安装包.exe' -Force
    Copy-Item build/linux-amd64/sphvideo dist/sphvideo-linux-amd64 -Force
    Copy-Item build/linux-arm64/sphvideo dist/sphvideo-linux-arm64 -Force
    Copy-Item build/linux-amd64/*.deb,build/linux-amd64/*.tar.gz,build/linux-arm64/*.deb,build/linux-arm64/*.tar.gz dist/
    tar -C build/darwin-arm64 -czf dist/sphvideo-macos-arm64.tar.gz sphvideo.app
    tar -C build/darwin-amd64 -czf dist/sphvideo-macos-amd64.tar.gz sphvideo.app
    Write-Output 'dist/sphvideo.exe'
    Write-Output 'dist/视频号视频下载-安装包.exe'
    Write-Output 'dist/sphvideo-linux-amd64'
    Write-Output 'dist/sphvideo-linux-arm64'
    Write-Output 'dist/sphvideo-macos-arm64.tar.gz'
    Write-Output 'dist/sphvideo-macos-amd64.tar.gz'

clean:
    Remove-Item -Recurse -Force dist,build,build-test -ErrorAction SilentlyContinue
