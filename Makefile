APP_NAME=nextcloud-gtk

build:
	go build -mod=readonly -o $(APP_NAME) main.go app_window.go

run: build
	./$(APP_NAME) --debug

clean:
	rm -f $(APP_NAME)
