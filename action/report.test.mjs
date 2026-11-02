// Unit tests for the Action's reporter. They run report.mjs as a child process,
// exactly as the runner does — environment variables in, exit code and step
// outputs out — because the interesting parts are the exit code and the output
// contract, and the action cannot assert those from inside its own process.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const reporter = join(here, 'report.mjs');

const doc = (name, fatal = 0, warn = 0, findings = []) => ({
	name,
	syntax: 'cii',
	valid: fatal === 0,
	fatal,
	warn,
	findings,
});

const artefacts = {
	engine: '9.9.9',
	rule_sets: [
		{ syntax: 'CII', version: '1.3.17', updated: '2026-10-09' },
		{ syntax: 'UBL', version: '1.3.17', updated: '2026-10-09' },
	],
	cius: {},
	profiles: {},
};

// run invokes the reporter the way the action does and returns what the runner
// would see: the exit code, the step outputs and stdout.
function run({ documents, artefactsPath = '', failOn = 'fatal', annotations = 300 }) {
	const dir = mkdtempSync(join(tmpdir(), 'report-'));
	const report = join(dir, 'report.json');
	const out = join(dir, 'output');
	const summary = join(dir, 'summary.md');
	writeFileSync(report, typeof documents === 'string' ? documents : JSON.stringify(documents));
	writeFileSync(out, '');
	writeFileSync(summary, '');
	// Diagnostics go to stderr (the ::error workflow commands have to be lines of
	// their own), so both streams are captured and searched together.
	const r = spawnSync(process.execPath, [reporter, report, failOn, artefactsPath], {
		cwd: dir,
		env: { ...process.env, GITHUB_OUTPUT: out, GITHUB_STEP_SUMMARY: summary },
		encoding: 'utf8',
	});
	const { status: code = 0 } = r;
	const stdout = `${r.stdout ?? ''}${r.stderr ?? ''}`;
	const outputs = Object.fromEntries(
		readFileSync(out, 'utf8')
			.split('\n')
			.filter(Boolean)
			.map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]),
	);
	return { code, outputs, stdout, summary: readFileSync(summary, 'utf8') };
}

test('counts, and no citation when there is no artefact report', () => {
	const r = run({ documents: [doc('a.xml', 2, 1), doc('b.xml')] });
	assert.equal(r.code, 1);
	assert.deepEqual(r.outputs, { files: '2', fatal: '2', warn: '1' });
	assert.equal(r.summary, '');
});

test('a run without a CIUS cites the base rule sets only', () => {
	const dir = mkdtempSync(join(tmpdir(), 'artefacts-'));
	const path = join(dir, 'artefacts.json');
	writeFileSync(path, JSON.stringify(artefacts));
	const r = run({ documents: [doc('a.xml', 1)], artefactsPath: path });
	assert.equal(r.outputs.engine, '9.9.9');
	assert.equal(r.outputs.rule_sets, 'CII 1.3.17, UBL 1.3.17');
	assert.match(r.summary, /InvoiceKit\*\* checked 1 document\(s\) — engine 9\.9\.9 · CII 1\.3\.17, UBL 1\.3\.17/);
});

test('a CIUS or profile the run applied has to appear in the citation', () => {
	// The bug this guards: the action asked for a CIUS and cited the base rule
	// sets anyway, which is a verdict whose provenance cannot be reproduced.
	const dir = mkdtempSync(join(tmpdir(), 'artefacts-'));
	const path = join(dir, 'artefacts.json');
	writeFileSync(path, JSON.stringify({
		...artefacts,
		cius: { xrechnung: '2.6.0, for XRechnung 3.0.2' },
		profiles: { peppol: 'Peppol BIS Billing 3.0 release 3.0.21' },
	}));
	const r = run({ documents: [doc('a.xml', 1)], artefactsPath: path });
	assert.equal(
		r.outputs.rule_sets,
		'CII 1.3.17, UBL 1.3.17; CIUS xrechnung 2.6.0, for XRechnung 3.0.2; profile peppol Peppol BIS Billing 3.0 release 3.0.21',
	);
	assert.match(r.stdout, /engine 9\.9\.9 · CII 1\.3\.17, UBL 1\.3\.17 · CIUS xrechnung/);
});

test('a corrupt artefact report costs the citation, not the verdict', () => {
	const dir = mkdtempSync(join(tmpdir(), 'artefacts-'));
	const path = join(dir, 'artefacts.json');
	writeFileSync(path, 'not json');
	const r = run({ documents: [doc('a.xml', 3)], artefactsPath: path });
	assert.equal(r.code, 1);
	assert.equal(r.outputs.files, '1');
	assert.equal(r.outputs.fatal, '3');
	assert.equal(r.outputs.engine, undefined);
	assert.match(r.stdout, /could not read the artefact report/);
});

test('fail-on decides the exit code, not the findings alone', () => {
	const warnOnly = [doc('a.xml', 0, 4)];
	const withFatal = [doc('a.xml', 1, 4)];
	assert.equal(run({ documents: warnOnly, failOn: 'fatal' }).code, 0);
	assert.equal(run({ documents: warnOnly, failOn: 'any' }).code, 1);
	assert.equal(run({ documents: withFatal, failOn: 'never' }).code, 0);
	assert.equal(run({ documents: withFatal, failOn: 'fatal' }).code, 1);
	assert.equal(run({ documents: [doc('a.xml')], failOn: 'any' }).code, 0);
});

test('a files input matching nothing is an error, not a clean run', () => {
	const r = run({ documents: [], failOn: 'never' });
	assert.equal(r.code, 2);
	assert.match(r.stdout, /no documents matched/);
});

test('unreadable report JSON fails loudly with its own code', () => {
	const r = run({ documents: '{', failOn: 'never' });
	assert.equal(r.code, 2);
	assert.match(r.stdout, /could not read the validator.s report/);
});

test('the annotation budget truncates output but never the counts', () => {
	const findings = Array.from({ length: 400 }, (_, i) => ({
		id: `BR-${i}`,
		flag: i % 2 === 0 ? 'fatal' : 'warning',
		message: `finding ${i}`,
	}));
	const r = run({ documents: [doc('a.xml', 200, 200, findings)] });
	const annotated = r.stdout.split('\n').filter((l) => l.startsWith('::error ') || l.startsWith('::warning ')).length;
	assert.equal(annotated, 300);
	assert.equal(r.outputs.fatal, '200');
	assert.equal(r.outputs.warn, '200');
	assert.match(r.stdout, /100 further finding\(s\) are in the JSON report, not annotated/);
});
