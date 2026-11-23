# Run goCrawler from any directory by resolving the Go module root first.
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root
& go run crawler.go @args
exit $LASTEXITCODE
