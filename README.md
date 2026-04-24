# Nextcloud GTK

A lightweight GTK4-based desktop file synchronization client for Nextcloud servers. It enables bidirectional sync between local directories and remote Nextcloud instances via WebDAV, with a clean native Linux UI.

**Repository:** [shinyvision/furios-nextcloud](https://github.com/shinyvision/furios-nextcloud)

![Application ID](io.github.shinyvision.NextcloudGtk)

## Features

- **Bidirectional sync** between local folders and Nextcloud via WebDAV
- **3-phase sync engine** with conflict detection (remote wins)
- **Real-time file watching** using `fsnotify` with debounced updates
- **Parallel transfers** with 5 upload and 5 download workers
- **OAuth2 login** flow with browser integration
- **GTK4 native UI** with icon and CSS theming support
- **Background daemon** architecture with IPC over Unix sockets

## Requirements

### Build (Native)

- **Go** 1.24.4 or later
- **GTK4** development libraries (`libgtk-4-dev` or equivalent)
- **libsqlite3** development libraries
- `pkg-config`

### Runtime

- GTK4
- libsqlite3
- A running Nextcloud instance with WebDAV enabled

## Building from Source

### Option 1: Native Build

```bash
# Build the binary
go build -mod=readonly -o nextcloud-gtk .

# Or using the Makefile
make build

# Run with debug logging
make run
# or
./nextcloud-gtk --debug
```

### Option 2: Flatpak (Recommended)

This project includes a Flatpak manifest for sandboxed distribution.

#### Prerequisites

Install the required Flatpak runtime and SDK:

```bash
flatpak remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
flatpak install flathub org.gnome.Platform//49 org.gnome.Sdk//49
flatpak install flathub org.freedesktop.Sdk.Extension.golang//24.08
```

> The manifest uses GNOME runtime **49** and the Freedesktop Go SDK extension.

#### Local Install

Run the provided install script (builds and installs to your user Flatpak):

```bash
./install.sh
```

Or run the commands manually:

```bash
flatpak-builder \
  --user \
  --install \
  --force-clean \
  --disable-rofiles-fuse \
  --install-deps-from=flathub \
  build-dir \
  io.github.shinyvision.NextcloudGtk.yml
```

Then launch the app:

```bash
flatpak run io.github.shinyvision.NextcloudGtk --debug
```

#### Export as a Bundle

To create a standalone `.flatpak` bundle for distribution:

```bash
flatpak-builder \
  --repo=repo \
  --force-clean \
  --disable-rofiles-fuse \
  build-dir \
  io.github.shinyvision.NextcloudGtk.yml

flatpak build-bundle repo nextcloud-gtk.flatpak io.github.shinyvision.NextcloudGtk
```

Install from the bundle:

```bash
flatpak install --user nextcloud-gtk.flatpak
```

#### Clean Build Files

```bash
rm -rf build-dir repo .flatpak-builder
```

## Project Structure

```
nextcloud-gtk/
├── main.go                 # GTK application entry point
├── app_window.go           # Main window and page navigation
├── daemon/                 # Background sync service
│   ├── daemon.go           # IPC command handling
│   └── sync.go             # Core sync engine
├── internal/
│   ├── nextcloud/client.go # WebDAV / OAuth2 client
│   └── ipc/ipc.go          # Unix socket IPC
├── storage/db.go           # SQLite database layer
├── ui/                     # GTK UI pages and components
├── assets/                 # CSS and icons
├── vendor/                 # Vendored Go dependencies
├── Makefile                # Native build & install targets
└── io.github.shinyvision.NextcloudGtk.yml  # Flatpak manifest
```

## Configuration & Data Paths

| File / Path | Location |
|-------------|----------|
| SQLite database | `~/.config/nextcloud-gtk/settings.db` |
| IPC socket | `${XDG_RUNTIME_DIR}/nextcloud-gtk/daemon.sock` |
| System assets (Flatpak) | `/app/share/nextcloud-gtk/` |

## Sync Behavior & Notes

- **Conflict resolution:** Remote always wins when both versions changed.
- **Ignored files:** Hidden files (starting with `.`) and temporary files (`~`, `.tmp`, `.temp`) are skipped.
- **Poll interval:** Remote changes are polled every 60 seconds.
- **Tombstones:** Deleted files are tracked for 30 days to prevent re-uploads.
- **Large files:** SHA256 hashing is used; transfers of 100 MB+ may be slow and do not support resume.

## Debugging

Run with the `--debug` flag for verbose logging:

```bash
./nextcloud-gtk --debug
# or
flatpak run io.github.shinyvision.NextcloudGtk --debug
```

## License

MIT
