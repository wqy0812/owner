import type { ComponentType } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { AppProvider } from '../context/AppContext';
import { installReadModelFixtures } from './fixtures/appFixtures';
import { render } from './render';

/** Page integration keeps real routing and data providers without mounting the shell or unrelated routes. */
export function pageRenderer(Page: ComponentType, route: string) {
  return (path = route, explicitReadModels = false) => {
    if (!explicitReadModels) installReadModelFixtures();
    return render(<MemoryRouter initialEntries={[path]}><AppProvider><Routes>
      <Route path={route} element={<Page />} />
    </Routes></AppProvider></MemoryRouter>);
  };
}
