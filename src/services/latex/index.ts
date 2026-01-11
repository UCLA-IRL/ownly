import { EngineStatus, PdfTeXEngine } from './PdfTeXEngine';
import { Toast } from '@/utils/toast';

import type { WorkspaceProj } from '../workspace-proj';

let activeProject: string | null = null;
let activeEngine: PdfTeXEngine | null = null;
// the original endpoint isn't working
// check what https://texlyre.github.io/texlyre/ uses if this doesn't work
//
// original
// let texlive_endpoint = 'https://texlive2.swiftlatex.com';
// 2nd try
// let texlive_endpoint = 'https://texlive.emaily.re/';
// 3rd try
let texlive_endpoint = 'https://texlive.texlyre.org';

export async function compile(project: WorkspaceProj): Promise<Uint8Array> {
  let progress: typeof Toast.Handle | undefined;

  try {
    // Fail fast if main.tex is not found
    const fileList = project.getFileList();
    if (!fileList.some((file) => file.path === '/main.tex')) {
      throw new Error('main.tex not found at root of project');
    }

    // Sync the project to OPFS
    progress = Toast.loading('Synchronizing project');
    const root = await project.syncFs();

    // Load the engine if it's not already loaded
    if (!activeEngine || activeProject !== project.uuid) {
      await progress.msg('Loading LaTeX engine');

      activeEngine?.closeWorker();
      activeEngine = new PdfTeXEngine();
      await activeEngine.loadEngine();
      activeEngine.setTexliveEndpoint(texlive_endpoint);
      activeProject = project.uuid;
    }

    // Compile the project
    // TODO: show progress for individual steps in compilation
    const defaultStatus = 'Compiling LaTeX project';
    await progress.msg(defaultStatus);
    const res = await activeEngine.compileLaTeX(root, 'main.tex', (status) => {
      progress?.msg(status ?? defaultStatus);
    });
    if (res.status == EngineStatus.Error) {
      throw new Error('Engine Error');
    } else if (res.pdf === undefined) {
      if (res.log) throw new Error(res.log);
      throw new Error('Engine produced no PDF');
    }

    progress.success('Compiled successfully', 1000);
    return res.pdf;
  } catch (err) {
    progress?.dismiss();
    throw err;
  }
}
