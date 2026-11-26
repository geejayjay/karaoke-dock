# Starts KaraokeDock. Extra arguments go to the server, such as -port 7465.
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
& (Join-Path $root 'bin\karaokedock.exe') --metadata (Join-Path $root 'data\metadata') --sidecar (Join-Path $root 'sidecar') @args
