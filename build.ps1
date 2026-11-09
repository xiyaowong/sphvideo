$env:GOPROXY = 'https://goproxy.cn,direct'
go build -trimpath -tags mygo_noinspector -ldflags "-s -w -X github.com/egoist/mygo.production=1" -o sphvideo.exe .
if ($LASTEXITCODE -eq 0) { Write-Output "built: $PWD\sphvideo.exe" }
