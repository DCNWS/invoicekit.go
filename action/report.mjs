#!/usr/bin/env node
// Turns the `-json` output of `einv validate` into workflow annotations and
// step outputs. It lives in a file rather than inline YAML so it is lintable,
// testable and reviewable as the program it is.
import { appendFileSync, readFileSync } from "node:fs";

const [report, failOn = "fatal"] = process.argv.slice(2);

const emit = (name, value) => {
	if (!process.env.GITHUB_OUTPUT) return;
	appendFileSync(process.env.GITHUB_OUTPUT, `${name}=${value}\n`);
};

let documents;
try {
	documents = JSON.parse(readFileSync(report, "utf8"));
} catch (e) {
	console.error(`::error::could not read the validator's report: ${e.message}`);
	process.exit(2);
}
if (!Array.isArray(documents)) {
	console.error("::error::the validator's report was not a list of documents");
	process.exit(2);
}

// A workflow run shows a handful of annotations and truncates the rest, so the
// budget is spent on the first findings and the remainder is counted instead of
// printed. The counts are never affected.
const annotationLimit = 300;
let annotations = 0;

let fatal = 0;
let warn = 0;
for (const doc of documents) {
	fatal += doc.fatal ?? 0;
	warn += doc.warn ?? 0;
	for (const f of doc.findings ?? []) {
		if (annotations >= annotationLimit) continue;
		annotations++;
		const level = f.flag === "fatal" || f.flag === "error" ? "error" : "warning";
		const title = `${f.id}${f.source ? ` (${f.source})` : ""}`;
		// The context is the XPath the rule speaks about, which is as close to
		// a line number as an XML rule set gets.
		console.log(`::${level} title=${title}::${doc.name}: ${f.message}${f.context ? ` — ${f.context}` : ""}`);
	}
	console.log(`${doc.name}: ${doc.syntax} ${doc.valid ? "valid" : `invalid (${doc.fatal} fatal, ${doc.warn} warning)`}`);
}

emit("files", documents.length);
emit("fatal", fatal);
emit("warn", warn);

if (documents.length === 0) {
	console.error("::error::no documents matched the files input");
	process.exit(2);
}
if (annotations < fatal + warn) {
	console.log(`${fatal + warn - annotations} further finding(s) are in the JSON report, not annotated.`);
}
const shouldFail = failOn === "any" ? fatal + warn > 0 : failOn === "never" ? false : fatal > 0;
console.log(`checked ${documents.length} document(s): ${fatal} fatal, ${warn} warning, fail-on=${failOn}`);
process.exit(shouldFail ? 1 : 0);
