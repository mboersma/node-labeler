build:
	GOOS=linux GOARCH=amd64 go build -a -tags netgo -ldflags '-w' -o node-labeler *.go
	upx node-labeler
	chmod +x node-labeler
	docker build -t quay.io/mboersma/node-labeler:new .
	docker push quay.io/mboersma/node-labeler:new
