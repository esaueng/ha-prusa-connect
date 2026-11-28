# ha-prusa-connect

Run your fork as a local app on Home Assistant OS. This app is built on Prusa
Connect by Prusa Research. It includes the server and web app. You can install
optional extensions from Prusa Connect's marketplace.

Download an archive from
[GitHub Releases](https://github.com/esaueng/ha-prusa-connect/releases).
The [Home Assistant app workflow](https://github.com/esaueng/ha-prusa-connect/actions/workflows/home-assistant-app.yml)
builds and tests `main` daily at 08:43 UTC. It publishes the current commit
after both archives pass their tests. Each commit can have one release.
GitHub can disable the schedule after 60 days without repository activity.
To build an archive from a checkout, run the following from the repository
root with Docker Buildx. Use `amd64` for an Intel or AMD system.

```sh
sh scripts/package-home-assistant.sh aarch64 /tmp/ha-prusa-connect
```

## Install the app

1. Download the archive for your system. Use `aarch64` on 64-bit ARM or
   `amd64` on 64-bit Intel and AMD. Find the CPU architecture under
   **Settings > System > Repairs > ⋮ > System information**.
2. Extract the archive. Copy the entire `ha-prusa-connect` folder into
   Home Assistant's `local_apps` folder. Some versions of the Samba Share
   app show the folder as `addons`.
3. Go to **Settings > Apps > Install app**. Reload the app store from the
   menu if needed. In **Local apps**, select **ha-prusa-connect** and install it.
4. Start the app. Open its web UI using the Home Assistant box's LAN address
   on port `8080`. The app's **Log** tab shows the one-time setup code.
5. Complete first-run setup with sign-in and printer approval enabled.

## Connect a printer

In Prusa Connect, open **Add a printer** and download the USB settings file.
Check that it gives the printer the Home Assistant box's LAN address and
published port. Load it on the printer and approve the pairing code.

The app uses the Home Assistant host network so a browser on the local
network can receive live camera video. Keep port `8080` available on the
host for the web app and the default printer connection.

Use the first-run wizard's encryption step to turn on HTTPS for the web app
or TLS for the printers. For a dedicated printer listener, choose port
`8443` in the wizard and keep it available on the host before pairing.
If you enable web HTTPS, open the app's `https://` address directly.

## Pair a Buddy3D camera

The app includes the Buddy3D camera extension. On a new installation, open
**Settings > Extensions** in Prusa Connect and install **Buddy3D camera**.
Approve TCP port `9443` so the camera can reach the app on the local network.

For an existing installation, use a Home Assistant host shell with Docker
access. Add the following entry under `core` in `/data/prusa-connect/config.yaml`
inside the app container. If `modules_local` already exists, add the entry
to that mapping. Keep the file owned by `prusa:prusa` with mode `0600` and
restart the app.

```yaml
core:
  modules_local:
    github.com/prusa3d/buddy3d-camera: /opt/modules/buddy3d-camera
```

In the extension settings, use the Home Assistant host's LAN address as
the address the camera dials. Keep `9443` free on that host. In the printer's
**Cameras** tab, add a camera of type **Buddy3D camera** and follow its
pairing instructions. The camera needs firmware `3.1.7` or later and a
FAT32 microSD card for the pairing file. The browser must be able to reach
the camera on the network to play its WebRTC live view.

## Back up and update

The app stores its configuration, database and print files in its private
`/data/prusa-connect` folder. Home Assistant includes the folder in app
backups. A host administrator can edit `config.yaml` inside the app
container and restart the app for advanced changes after setup.
Keep file and database paths within the app's data folder.

Check the listener ports before updating or rebuilding from a new archive.
Make sure other services leave `8080` and any dedicated printer port free
on the host. Host networking uses the server's listener ports directly,
so the app cannot start if a port is already in use.

The web app and default printer connection listen on `8080` on the host,
even when the old mapping was blank. If you left the mapping blank to keep
the service off the local network, keep your current version until you can
restrict access to that port on the host.

If you set a custom web port under **Configuration > Network** in an earlier
version, restore it to `8080` before updating. This updates the **Open Web
UI** button. If a printer used a different mapped port, update its
connection address to the port the server listens on.

A dedicated printer listener becomes reachable on the host even when its
old port mapping was blank. To keep it private, set
`core.connectors.<name>.config.listener.addr` to `localhost:8443` in the
server configuration before updating. Use the listener's current port
if it differs from `8443`.

Back up the app before replacing its files in `local_apps`. Reload the app
store and apply the update. For an archive with the same app version, use
**Rebuild** from the app's menu. Keep the same folder and app slug so Home
Assistant uses the existing app data.
