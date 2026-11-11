import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { PutObjectCommand, ListObjectsV2Command, ListObjectsCommand } from "@aws-sdk/client-s3";
import { client, uniqueBucket, createBucket, emptyAndDelete, closeClients } from "./lib.mjs";

const Bucket = uniqueBucket("list");
const N = 2500;
const allKeys = [];

before(async () => {
  await createBucket(Bucket);
  for (let i = 0; i < N; i++) allKeys.push(`many/${String(i).padStart(5, "0")}`);
  allKeys.push("dir/a/1", "dir/a/2", "dir/b/1", "dir/c", "top", "sp ace/x");
  const queue = [...allKeys];
  await Promise.all(Array.from({ length: 16 }, async () => {
    for (let k = queue.shift(); k !== undefined; k = queue.shift()) {
      await client.send(new PutObjectCommand({ Bucket, Key: k, Body: "x" }));
    }
  }));
  allKeys.sort();
});
after(async () => {
  await emptyAndDelete(Bucket);
  closeClients();
});

test("prefix", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "dir/a/" }));
  assert.deepEqual(out.Contents.map((o) => o.Key), ["dir/a/1", "dir/a/2"]);
  assert.equal(out.KeyCount, 2);
  assert.equal(out.IsTruncated, false);
  assert.equal(out.Contents[0].Size, 1);
  assert.equal(out.Contents[0].StorageClass, "STANDARD");
});

test("delimiter and CommonPrefixes", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "dir/", Delimiter: "/" }));
  assert.deepEqual(out.Contents.map((o) => o.Key), ["dir/c"]);
  assert.deepEqual(out.CommonPrefixes.map((p) => p.Prefix), ["dir/a/", "dir/b/"]);
  const root = await client.send(new ListObjectsV2Command({ Bucket, Delimiter: "/" }));
  assert.deepEqual(root.CommonPrefixes.map((p) => p.Prefix), ["dir/", "many/", "sp ace/"]);
  assert.deepEqual(root.Contents.map((o) => o.Key), ["top"]);
});

test("max-keys pagination across 2500 keys", async () => {
  const seen = [];
  let ContinuationToken;
  let pages = 0;
  do {
    const out = await client.send(new ListObjectsV2Command({ Bucket, MaxKeys: 700, ContinuationToken }));
    assert.ok(out.KeyCount <= 700);
    seen.push(...out.Contents.map((o) => o.Key));
    ContinuationToken = out.NextContinuationToken;
    pages++;
  } while (ContinuationToken);
  assert.equal(pages, Math.ceil(allKeys.length / 700));
  assert.deepEqual(seen, allKeys);
});

test("default page size is 1000", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket }));
  assert.equal(out.KeyCount, 1000);
  assert.equal(out.IsTruncated, true);
});

test("continuation through a common prefix", async () => {
  const pages = [];
  let ContinuationToken;
  do {
    const out = await client.send(new ListObjectsV2Command({ Bucket, Delimiter: "/", MaxKeys: 1, ContinuationToken }));
    pages.push([...(out.CommonPrefixes ?? []).map((p) => p.Prefix), ...(out.Contents ?? []).map((o) => o.Key)]);
    ContinuationToken = out.NextContinuationToken;
  } while (ContinuationToken);
  assert.deepEqual(pages.flat(), ["dir/", "many/", "sp ace/", "top"]);
  assert.ok(pages.every((p) => p.length === 1));
});

test("start-after", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "dir/", StartAfter: "dir/a/2" }));
  assert.deepEqual(out.Contents.map((o) => o.Key), ["dir/b/1", "dir/c"]);
  assert.equal(out.StartAfter, "dir/a/2");
});

test("encoding-type url decodes transparently", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "sp ace/", EncodingType: "url" }));
  assert.equal(out.EncodingType, "url");
  // The SDK does not decode; the wire value is URL-encoded.
  assert.deepEqual(out.Contents.map((o) => decodeURIComponent(o.Key.replace(/\+/g, " "))), ["sp ace/x"]);
  assert.notEqual(out.Contents[0].Key, "sp ace/x");
});

test("fetch-owner", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "top", FetchOwner: true }));
  assert.ok(out.Contents[0].Owner?.ID);
  const without = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "top" }));
  assert.equal(without.Contents[0].Owner, undefined);
});

test("max-keys 0", async () => {
  const out = await client.send(new ListObjectsV2Command({ Bucket, MaxKeys: 0 }));
  assert.equal(out.KeyCount, 0);
  assert.equal(out.Contents, undefined);
});

test("v1 prefix", async () => {
  const out = await client.send(new ListObjectsCommand({ Bucket, Prefix: "dir/a/" }));
  assert.deepEqual(out.Contents.map((o) => o.Key), ["dir/a/1", "dir/a/2"]);
  assert.equal(out.IsTruncated, false);
  // V1 carries the owner on every object without asking for it.
  assert.ok(out.Contents[0].Owner?.ID);
});

test("v1 delimiter and CommonPrefixes", async () => {
  const out = await client.send(new ListObjectsCommand({ Bucket, Prefix: "dir/", Delimiter: "/" }));
  assert.deepEqual(out.Contents.map((o) => o.Key), ["dir/c"]);
  assert.deepEqual(out.CommonPrefixes.map((p) => p.Prefix), ["dir/a/", "dir/b/"]);
});

test("v1 marker pagination across 2500 keys", async () => {
  const seen = [];
  let Marker;
  let pages = 0;
  do {
    const out = await client.send(new ListObjectsCommand({ Bucket, MaxKeys: 700, Marker }));
    seen.push(...out.Contents.map((o) => o.Key));
    // No delimiter, so S3 sends no NextMarker: resume from the last key.
    Marker = out.IsTruncated ? out.Contents.at(-1).Key : undefined;
    pages++;
  } while (Marker);
  assert.equal(pages, Math.ceil(allKeys.length / 700));
  assert.deepEqual(seen, allKeys);
});

test("v1 NextMarker is set when a delimiter is used", async () => {
  const pages = [];
  let Marker;
  do {
    const out = await client.send(new ListObjectsCommand({ Bucket, Delimiter: "/", MaxKeys: 1, Marker }));
    pages.push([...(out.CommonPrefixes ?? []).map((p) => p.Prefix), ...(out.Contents ?? []).map((o) => o.Key)]);
    Marker = out.NextMarker;
  } while (Marker);
  assert.deepEqual(pages.flat(), ["dir/", "many/", "sp ace/", "top"]);
  assert.ok(pages.every((p) => p.length === 1));
});
