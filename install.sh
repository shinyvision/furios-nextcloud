#!/usr/bin/env bash

go build -o nextcloud-gtk-bin .
flatpak-builder --user --install --force-clean --disable-rofiles-fuse --install-deps-from=flathub build-dir io.github.shinyvision.NextcloudGtk.yml
flatpak run io.github.shinyvision.NextcloudGtk --debug
