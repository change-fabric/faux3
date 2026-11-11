import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import {
  PutObjectCommand, DeleteObjectsCommand, HeadObjectCommand, ListObjectsV2Command,
} from "@aws-sdk/client-s3";
import { client, uniqueBucket, createBucket, emptyAndDelete, expectS3Error, closeClients } from "./lib.mjs";

const Bucket = uniqueBucket("delete");
before(() => createBucket(Bucket));
after(async () => {
  await emptyAndDelete(Bucket);
  closeClients();
});

async function put(keys) {
  for (const Key of keys) await client.send(new PutObjectCommand({ Bucket, Key, Body: "x" }));
}

test("batch delete reports every key, missing ones included", async () => {
  await put(["a", "b", "c"]);
  const out = await client.send(new DeleteObjectsCommand({
    Bucket, Delete: { Objects: [{ Key: "a" }, { Key: "b" }, { Key: "gone" }] },
  }));
  assert.deepEqual((out.Deleted ?? []).map((d) => d.Key).sort(), ["a", "b", "gone"]);
  assert.equal(out.Errors, undefined);
  await expectS3Error(assert, client.send(new HeadObjectCommand({ Bucket, Key: "a" })), "NotFound", 404);
  const left = await client.send(new ListObjectsV2Command({ Bucket }));
  assert.deepEqual((left.Contents ?? []).map((o) => o.Key), ["c"]);
});

test("quiet mode returns no Deleted entries", async () => {
  await put(["q1", "q2"]);
  const out = await client.send(new DeleteObjectsCommand({
    Bucket, Delete: { Quiet: true, Objects: [{ Key: "q1" }, { Key: "q2" }, { Key: "missing" }] },
  }));
  assert.equal(out.Deleted, undefined);
  assert.equal(out.Errors, undefined);
});

test("recursive delete clears a large prefix over multiple batches", async () => {
  const keys = Array.from({ length: 1200 }, (_, i) => `many/${String(i).padStart(4, "0")}`);
  for (let i = 0; i < keys.length; i += 100) {
    await Promise.all(keys.slice(i, i + 100).map((Key) => client.send(new PutObjectCommand({ Bucket, Key, Body: "x" }))));
  }
  // List returns at most 1000 keys per page, which is also the DeleteObjects limit.
  let ContinuationToken;
  do {
    const page = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "many/", ContinuationToken }));
    const Objects = (page.Contents ?? []).map((o) => ({ Key: o.Key }));
    if (Objects.length) await client.send(new DeleteObjectsCommand({ Bucket, Delete: { Objects, Quiet: true } }));
    ContinuationToken = page.NextContinuationToken;
  } while (ContinuationToken);
  const left = await client.send(new ListObjectsV2Command({ Bucket, Prefix: "many/" }));
  assert.equal(left.KeyCount, 0);
});

test("missing bucket fails the whole request", async () => {
  await expectS3Error(assert,
    client.send(new DeleteObjectsCommand({ Bucket: uniqueBucket("nope"), Delete: { Objects: [{ Key: "a" }] } })),
    "NoSuchBucket", 404);
});
