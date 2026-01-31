.PHONY: all install

APP_NAME=nextcloud-gtk
BIN_NAME=nextcloud-gtk-bin
APP_ID=io.github.shinyvision.NextcloudGtk


install:
	@if [ ! -f nextcloud-gtk-bin ]; then \
		go build -o nextcloud-gtk-bin .; \
	else \
		echo "Using existing binary: nextcloud-gtk-bin"; \
	fi
	@echo "Installing files..."
	install -D -m 755 nextcloud-gtk-bin /app/bin/nextcloud-gtk
	install -D -m 644 io.github.shinyvision.NextcloudGtk.desktop /app/share/applications/io.github.shinyvision.NextcloudGtk.desktop
	install -D -m 644 io.github.shinyvision.NextcloudGtk.metainfo.xml /app/share/metainfo/io.github.shinyvision.NextcloudGtk.metainfo.xml
	mkdir -p /app/share/nextcloud-gtk
	cp -r assets /app/share/nextcloud-gtk/
	install -D -m 644 assets/appicon.png /app/share/icons/hicolor/512x512/apps/io.github.shinyvision.NextcloudGtk.png
	install -D -m 644 assets/icons/16x16.png /app/share/icons/hicolor/16x16/apps/io.github.shinyvision.NextcloudGtk.png
	install -D -m 644 assets/icons/32x32.png /app/share/icons/hicolor/32x32/apps/io.github.shinyvision.NextcloudGtk.png
	install -D -m 644 assets/icons/64x64.png /app/share/icons/hicolor/64x64/apps/io.github.shinyvision.NextcloudGtk.png
	install -D -m 644 assets/icons/256x256.png /app/share/icons/hicolor/256x256/apps/io.github.shinyvision.NextcloudGtk.png
