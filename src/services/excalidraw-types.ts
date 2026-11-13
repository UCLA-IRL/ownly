import type * as Y from 'yjs';
// Keep Excalidraw 0.18's version hash local: importing the browser editor
// initializes DOM globals that are unavailable in headless Node.
const hashElementsVersion = (elements: readonly ExcalidrawElement[]): number =>
  elements.reduce((hash, element) => (hash * 33 + element.versionNonce) | 0, 5381) >>> 0;
import type { ExcalidrawElement, Theme } from '@excalidraw/excalidraw/element/types';
import type { AppState, BinaryFileData, BinaryFiles } from '@excalidraw/excalidraw/types';
import type { ImportedDataState } from '@excalidraw/excalidraw/data/types';
import type { Language } from '@excalidraw/excalidraw/i18n';

export type ExcalidrawElementYMap = Y.Map<ExcalidrawElement>;
export type ExcalidrawFilesYMap = Y.Map<BinaryFileData>;

/** The config passed to data-excalidraw-config as initial config to Excalidraw editor in BASE64 */
export type ExcalidrawConfig = {
  /** The content encoded in UTF-8 of JSON type ExportedDataState, containing type, elements and appState */
  content: Array<number>;
  /** Only support JSON for now */
  contentType: 'application/json';
  /** Is it view only or not */
  viewModeEnabled: boolean;
  /** Light or dark theme */
  theme: Theme;
  /** Not supported for now. JSON encoded type ExportedLibraryData */
  library: string;
  /** Not supported for now */
  imageParams: {
    exportBackground: boolean;
    exportWithDarkMode: boolean;
    exportScale: 1 | 2 | 3;
  };
  /** Not supported for now */
  langCode: Language['code'];
  /** Diagram name */
  name: string | undefined;
};

export const excalidrawToFile = (
  elements: readonly ExcalidrawElement[],
  appState?: AppState,
  files?: BinaryFiles,
): ImportedDataState => {
  return {
    elements,
    type: 'excalidraw',
    version: hashElementsVersion(elements),
    source: globalThis.origin,
    appState: appState ?? {
      gridSize: 20,
      gridStep: 5,
      gridModeEnabled: false,
      viewBackgroundColor: '#ffffff',
    },
    files: files ?? {},
  };
};
