import {mkdtemp, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {execFileSync} from 'node:child_process';

export default async function setup() {
  const dir = await mkdtemp(path.join(tmpdir(), 'kilo-e2e-binary-'));
  const binary = path.join(dir, process.platform === 'win32' ? 'kilo-e2e.exe' : 'kilo-e2e');
  try {
    execFileSync('go', ['test', '-c', '-o', binary], {stdio:'inherit', timeout:180000});
  } catch (error) {
    await rm(dir, {recursive:true,force:true});
    throw error;
  }
  process.env.KILO_E2E_BINARY = binary;
  return () => rm(dir, {recursive:true,force:true});
}
