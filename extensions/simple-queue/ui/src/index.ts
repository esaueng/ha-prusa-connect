// The bundle's entry. The app imports this file and calls what it default
// exports, handing it the one object a module receives.
import { defineModule } from '@prusa/connect-ui/slots';

import { apiOn } from './api';
import { grantsOn } from './grants';
import { OverviewStore } from './overview-store.svelte';
import { addPrinterFile, addUpload } from './device-file-action';
import { holdSession } from './session';
import { holdThumbnails } from './thumbnails';
import { PrinterQueueTab, QueueCard, QueueRoute, QueueSection } from './views';
import './styles.css';

export default defineModule((host) => {
  const api = apiOn(host);
  holdThumbnails(host.client);
  holdSession({
    host,
    api,
    grants: grantsOn(host),
    overview: new OverviewStore(api, host.client),
  });

  host.register.page('queue', QueueRoute);
  host.register.deviceTab('queue', PrinterQueueTab);
  host.register.deviceCard('queue', QueueCard);
  host.register.printSection('queue', QueueSection);
  host.register.deviceFileAction('queue', (ref) =>
    addPrinterFile(host, api, ref),
  );
  host.register.deviceUploadAction('queue', (ref) => addUpload(host, api, ref));
});
