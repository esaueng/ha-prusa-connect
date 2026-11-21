<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import { fakeDevice } from '../../../test/fakes';
  import PrinterFilesPanel from './printer-files-panel.svelte';
  import type {
    PrinterFileEntry,
    PrinterFilesSource,
    PrinterFileTransfers,
  } from './types';

  const { Story } = defineMeta({
    title: 'Parts/Screen components/Printer Detail/Files',
    component: PrinterFilesPanel,
  });

  const device = {
    ...fakeDevice('printer-1', 'MK4S #1042'),
    capabilities: [
      'core.command.start_print',
      'core.command.delete_file',
      'core.command.delete_folder',
    ],
    state: { '/state': 'IDLE' },
  };

  const commands = {
    onCommand: () => Promise.resolve({ kind: 'accepted' as const }),
  };

  const entries: PrinterFileEntry[] = [
    {
      folder: true,
      modified: '2026-09-10T08:00:00Z',
      name: 'CALIBR~1',
      display_name: 'calibration',
      path: 'usb/calibration',
      read_only: false,
      size: 0,
    },
    {
      folder: false,
      modified: '2026-09-12T10:30:00Z',
      name: 'BRACK~1.BGC',
      display_name: 'Bracket_Left_Reinforced_v4_FINAL.bgcode',
      path: 'usb/Bracket_Left_Reinforced_v4_FINAL.bgcode',
      read_only: false,
      size: 4_182_016,
    },
    {
      folder: false,
      modified: '2026-08-30T17:45:00Z',
      name: 'spool-holder.gcode',
      path: 'usb/spool-holder.gcode',
      read_only: false,
      size: 11_204_096,
    },
    {
      folder: false,
      modified: '2026-08-02T09:14:05Z',
      name: 'prusa_printer_settings.ini',
      path: 'usb/prusa_printer_settings.ini',
      read_only: true,
      size: 2048,
    },
  ];

  /** What the calibration folder holds, which only its expanded row lists. */
  const inside: PrinterFileEntry[] = [
    {
      folder: false,
      modified: '2026-09-09T11:20:00Z',
      name: 'first_layer.bgcode',
      path: 'usb/calibration/first_layer.bgcode',
      read_only: false,
      size: 524_288,
    },
    {
      folder: false,
      modified: '2026-09-09T11:22:00Z',
      name: 'temp_tower.bgcode',
      path: 'usb/calibration/temp_tower.bgcode',
      read_only: false,
      size: 1_048_576,
    },
  ];

  const source = (
    over: Partial<PrinterFilesSource> = {},
  ): PrinterFilesSource => ({
    directory: 'usb',
    entries: [],
    filesError: undefined,
    filesLoaded: true,
    filesLoading: false,
    filesReadingSince: undefined,
    filesUnread: false,
    filesRefreshing: false,
    expanded: {},
    expand: () => Promise.resolve(),
    collapse: () => undefined,
    changedAt: () => Promise.resolve(),
    load: () => Promise.resolve(),
    openFolder: () => Promise.resolve(),
    ...over,
  });

  const FLASH = 'core.command.flash';

  const firmware: PrinterFileEntry = {
    folder: false,
    modified: '2026-09-14T12:00:00Z',
    name: 'MK4S_firmware_6.3.2.bbf',
    path: 'usb/MK4S_firmware_6.3.2.bbf',
    read_only: false,
    size: 2_515_968,
  };

  /** A file the printer reported no modification time for. */
  const undated: PrinterFileEntry = {
    folder: false,
    name: 'recovered.bgcode',
    path: 'usb/recovered.bgcode',
    read_only: false,
    size: 1_048_576,
  };

  const refused = Object.assign(
    new Error('the printer is not answering on this address'),
    { code: 'core.device/NOT_FOUND' },
  );

  const CREATE_FOLDER = 'core.command.create_folder';

  /** A read the printer began a few seconds before the story opened. */
  const readingSince = new Date(Date.now() - 12_000).toISOString();

  const unfinished = Object.assign(
    new Error('the printer did not finish reading the folder'),
    { code: 'core.printer/LISTING_UNFINISHED' },
  );

  const receive = (): void => undefined;

  /** What the empty path answers: the drives themselves. */
  const drives: PrinterFileEntry[] = [
    {
      folder: true,
      name: 'usb',
      display_name: 'USB drive',
      path: 'usb',
      read_only: false,
      size: 0,
    },
    {
      folder: true,
      name: 'sd',
      display_name: 'SD card',
      path: 'sd',
      read_only: false,
      size: 0,
    },
  ];

  /** A source that can be walked. */
  const navigable = (): PrinterFilesSource => {
    const tree: Record<string, PrinterFileEntry[]> = {
      '': drives,
      usb: entries,
      'usb/calibration': inside,
      sd: [],
    };
    let directory = $state('');
    let expanded = $state.raw<
      Record<string, readonly PrinterFileEntry[] | undefined>
    >({});

    return {
      get directory() {
        return directory;
      },
      get entries() {
        return tree[directory] ?? [];
      },
      filesError: undefined,
      filesLoaded: true,
      filesLoading: false,
      filesReadingSince: undefined,
      filesUnread: false,
      filesRefreshing: false,
      get expanded() {
        return expanded;
      },
      expand: (path: string) => {
        expanded = { ...expanded, [path]: tree[path] ?? [] };

        return Promise.resolve();
      },
      collapse: (path: string) => {
        expanded = Object.fromEntries(
          Object.entries(expanded).filter(([held]) => held !== path),
        );
      },
      changedAt: () => Promise.resolve(),
      load: () => Promise.resolve(),
      openFolder: (path: string) => {
        directory = path;
        expanded = {};

        return Promise.resolve();
      },
    };
  };

  /** One of each state the store keeps a transfer in. */
  const transfers: PrinterFileTransfers = {
    current: [
      {
        transferId: '01M1GRZD931RAKEXTQ7V15GC1K',
        storage: 'local',
        source: 'models/Bracket_Left_Reinforced_v4_FINAL.bgcode',
        path: 'usb/Bracket_Left_Reinforced_v4_FINAL.bgcode',
        bytesTotal: 4_182_016,
        bytesSent: 1_703_936,
        state: 'sending',
        reason: undefined,
      },
      {
        transferId: '01M1GRZD931RAKEXTQ7V15GC2L',
        storage: 'local',
        source: 'models/spool-holder.gcode',
        path: 'usb/spool-holder.gcode',
        bytesTotal: 11_204_096,
        bytesSent: 11_204_096,
        state: 'done',
        reason: undefined,
      },
      {
        transferId: '01M1GRZD931RAKEXTQ7V15GC3M',
        storage: 'local',
        source: 'models/tall_vase.bgcode',
        path: 'usb/tall_vase.bgcode',
        bytesTotal: 8_388_608,
        bytesSent: 2_097_152,
        state: 'failed',
        reason: 'the printer closed the connection',
      },
    ],
    stop: () => Promise.resolve(),
  };

  /** More plates than one page holds, which is what the pager is drawn for. */
  const many: PrinterFileEntry[] = Array.from({ length: 30 }, (_, index) => ({
    folder: false,
    modified: '2026-09-01T06:00:00Z',
    name: `plate_${String(index + 1).padStart(3, '0')}.bgcode`,
    path: `usb/plate_${String(index + 1).padStart(3, '0')}.bgcode`,
    read_only: false,
    size: 1_048_576 * (index + 1),
  }));

  /** A file kept in a folder of the one storage, as the toggle draws it on. */
  const keep = {
    keep: true,
    onKeepChange: () => undefined,
    storages: [{ name: 'local', label: 'Local' }],
    storage: 'local',
    onStorageChange: () => undefined,
    folder: 'prints/brackets',
    onFolderChange: () => undefined,
    pickerAt: '',
    pickerFolders: [],
    onPickerWalk: () => undefined,
  };
</script>

<!-- A folder row wears a chevron. -->
<Story name="A Folder Of Files" asChild>
  <PrinterFilesPanel {device} {commands} files={source({ entries })} />
</Story>

<!-- A firmware image wears its kind as a badge and offers Flash where a. -->
<Story name="Firmware On The Drive" asChild>
  <PrinterFilesPanel
    device={{ ...device, capabilities: [...device.capabilities, FLASH] }}
    {commands}
    files={source({ entries: [...entries, firmware, undated] })}
  />
</Story>

<!-- A drive the printer reports and holds nothing in. -->
<Story name="An Empty Drive" asChild>
  <PrinterFilesPanel {device} {commands} files={source()} />
</Story>

<Story name="Loading" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ filesLoaded: false, filesLoading: true })}
  />
</Story>

<Story name="The Server Refused" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ filesError: refused })}
  />
</Story>

<!-- A drive the printer has not finished reading lists nothing yet. The tab
     says so and counts the seconds rather than waiting in silence. -->
<Story name="Reading The Drive" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ filesReadingSince: readingSince, filesUnread: true })}
  />
</Story>

<!-- The rows held from the last read stay while the printer reads again. -->
<Story name="Updating Rows" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries, filesReadingSince: readingSince })}
  />
</Story>

<Story name="Read Failed With Retry" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ filesError: unfinished })}
  />
</Story>

<!-- The empty path lists the storages themselves, and with no USB drive in
     the printer there are none. -->
<Story name="No Storage At All" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ directory: '' })}
    uploadBlock="noStorage"
    onRecheck={() => undefined}
  />
</Story>

<!-- The printer has not said yet what storages it holds. -->
<Story name="Storage Not Reported Yet" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ directory: '' })}
    uploadBlock="storagesUnknown"
    onRecheck={() => undefined}
  />
</Story>

<!-- A drive to browse, and no storage on this server to upload through. -->
<Story name="No Server Storage For Uploads" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    uploadBlock="noServerStorage"
  />
</Story>

<Story name="A Folder A New One Fits In" asChild>
  <PrinterFilesPanel
    device={{
      ...device,
      capabilities: [...device.capabilities, CREATE_FOLDER],
    }}
    {commands}
    files={source({ entries })}
  />
</Story>

<!-- More than one page of plates. -->
<Story name="More Than One Page" asChild>
  <PrinterFilesPanel {device} {commands} files={source({ entries: many })} />
</Story>

<Story name="Files Can Be Sent To The Drive" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
  />
</Story>

<Story name="Files On Their Way To The Printer" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
    {transfers}
  />
</Story>

<!-- A file dropped here goes up to the server before the printer fetches it. -->
<Story name="A File Going Up To The Server" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
    uploads={[
      {
        id: 'job:1',
        deviceId: device.id,
        printerName: device.name,
        name: 'gasket_set_tpu.bgcode',
        step: 'uploading',
        progress: 0.4,
        mine: true,
        startedBy: undefined,
        canCancel: true,
        stopping: false,
      },
    ]}
    onCancelUpload={() => undefined}
  />
</Story>

<!-- Files dropped together go to the printer one after another. -->
<Story name="Files Waiting For The Printer" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
    uploads={[
      {
        id: 'job:1',
        deviceId: device.id,
        printerName: device.name,
        name: 'gasket_set_tpu.bgcode',
        step: 'sending',
        progress: null,
        mine: true,
        startedBy: undefined,
        canCancel: false,
        stopping: false,
      },
      {
        id: 'job:2',
        deviceId: device.id,
        printerName: device.name,
        name: 'spool_holder.bgcode',
        step: 'waiting',
        progress: null,
        mine: true,
        startedBy: undefined,
        canCancel: true,
        stopping: false,
      },
    ]}
    onCancelUpload={() => undefined}
  />
</Story>

<!-- The printer holds a file under the first name, so the person is asked
     and the next file keeps waiting behind it. -->
<Story name="A File Waiting For An Answer" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
    uploads={[
      {
        id: 'job:1',
        deviceId: device.id,
        printerName: device.name,
        name: 'gasket_set_tpu.bgcode',
        step: 'asking',
        progress: null,
        mine: true,
        startedBy: undefined,
        canCancel: false,
        stopping: false,
      },
      {
        id: 'job:2',
        deviceId: device.id,
        printerName: device.name,
        name: 'spool_holder.bgcode',
        step: 'waiting',
        progress: null,
        mine: true,
        startedBy: undefined,
        canCancel: true,
        stopping: false,
      },
    ]}
    onCancelUpload={() => undefined}
  />
</Story>

<!-- A transfer outlives the tab it was started from. -->
<Story name="A Transfer With No Way To Add To It" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    {transfers}
  />
</Story>

<Story name="Two Drives To Choose Between" asChild>
  <PrinterFilesPanel
    device={{
      ...device,
      capabilities: [...device.capabilities, CREATE_FOLDER],
    }}
    {commands}
    files={navigable()}
    onUpload={receive}
  />
</Story>

<!-- The person keeps the file in a storage folder as well as sending it. -->
<Story name="Files Kept On The Server As Well" asChild>
  <PrinterFilesPanel
    {device}
    {commands}
    files={source({ entries })}
    onUpload={receive}
    {keep}
  />
</Story>
