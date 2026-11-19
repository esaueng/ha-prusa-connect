# ha-prusa-connect

Run your fork as a local app on Home Assistant OS. This app is built on Prusa
Connect by Prusa Research. It includes the server and web app. You can install
optional extensions from Prusa Connect's marketplace.

Get the archive from a successful
[Home Assistant app build](https://github.com/esaueng/ha-prusa-connect/actions/workflows/home-assistant-app.yml).
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

You can change the LAN port under the app's **Configuration > Network** if
another service uses `8080`. The app always listens on port `8080` inside the
container.

Use the first-run wizard's encryption step to turn on HTTPS for the web app
or TLS for the printers. For a dedicated printer listener, choose port
`8443` in the wizard and enable the `8443/tcp` mapping before pairing.
If you enable web HTTPS, open the app's `https://` address directly.

## Back up and update

The app stores its configuration, database and print files in its private
`/data/prusa-connect` folder. Home Assistant includes the folder in app
backups. A host administrator can edit `config.yaml` inside the app
container and restart the app for advanced changes after setup.
Keep file and database paths within the app's data folder.

Back up the app before replacing its files in `local_apps`. Reload the app
store and apply the update. For an archive with the same app version, use
**Rebuild** from the app's menu. Keep the same folder and app slug so Home
Assistant uses the existing app data.
