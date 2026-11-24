// The bundle's entry. The app imports this file and calls what it default
// exports, handing it the one object a module receives.
import { defineModule } from '@prusa/connect-ui/slots';

import { apiOn } from './api';
import { holdSession } from './session';
import { SlicerRoute } from './views';
import './styles.css';

export default defineModule((host) => {
  holdSession({ host, api: apiOn(host) });

  host.register.page('slicer', SlicerRoute);

  // The Slice button on a project's page opens the slice dialog on this
  // module's page. The router reads the address on popstate only.
  const openFile = (query: URLSearchParams): Promise<void> => {
    const href = `${host.pageHref('slicer')}?${query}`;
    window.history.pushState(null, '', href);
    window.dispatchEvent(new PopStateEvent('popstate'));

    return Promise.resolve();
  };
  host.register.fileAction('slice', (fileRef) =>
    openFile(new URLSearchParams({ slice: fileRef })),
  );
  host.register.fileAction('prepare', (fileRef) =>
    openFile(new URLSearchParams({ tab: 'prepare', model: fileRef })),
  );
});
