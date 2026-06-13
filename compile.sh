go build -ldflags "-H windowsgui" -o AdobeUpdateWindows_x64.exe
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o AdobeUpdateLinux_amd64
Remove-Item Env:GOOS
Remove-Item Env:GOARCH
