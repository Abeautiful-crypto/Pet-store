# 本地启动：MySQL 真相源 + Redis 缓存 + 高保真前端
# 用法：在仓库根目录执行  powershell -File scripts/start-local.ps1
$env:APP_ENV = 'dev'
$env:STORE_DRIVER = 'mysql'
$env:MYSQL_DSN = 'root:root@tcp(127.0.0.1:3306)/pet_store?parseTime=true&charset=utf8mb4'
$env:REDIS_ADDR = '127.0.0.1:6379'
$env:HTTP_ADDR = ':8080'
$env:PETSTORE_TEST_DSN = $env:MYSQL_DSN
$env:PETSTORE_TEST_REDIS_ADDR = $env:REDIS_ADDR

Write-Host 'MySQL  : 127.0.0.1:3306/pet_store'
Write-Host 'Redis  : 127.0.0.1:6379'
Write-Host 'Web    : http://127.0.0.1:8080/'
go run ./cmd/server
