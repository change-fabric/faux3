import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import {
  CopyObjectCommand, DeleteObjectsCommand, CreateMultipartUploadCommand, PutBucketVersioningCommand,
  GetObjectAclCommand, ListObjectsCommand, PutObjectCommand,
} from "@aws-sdk/client-s3";
import { client, uniqueBucket, createBucket, emptyAndDelete, expectS3Error, closeClients } from "./lib.mjs";

const Bucket = uniqueBucket("unsupported");
before(async () => {
  await createBucket(Bucket);
  await client.send(new PutObjectCommand({ Bucket, Key: "src", Body: "s" }));
});
after(async () => {
  await emptyAndDelete(Bucket);
  closeClients();
});

const cases = {
  CopyObject: () => new CopyObjectCommand({ Bucket, Key: "dst", CopySource: `${Bucket}/src` }),
  DeleteObjects: () => new DeleteObjectsCommand({ Bucket, Delete: { Objects: [{ Key: "src" }] } }),
  CreateMultipartUpload: () => new CreateMultipartUploadCommand({ Bucket, Key: "mp" }),
  PutBucketVersioning: () => new PutBucketVersioningCommand({ Bucket, VersioningConfiguration: { Status: "Enabled" } }),
  GetObjectAcl: () => new GetObjectAclCommand({ Bucket, Key: "src" }),
  ListObjectsV1: () => new ListObjectsCommand({ Bucket }),
  PutObjectSSE: () => new PutObjectCommand({ Bucket, Key: "sse", Body: "x", ServerSideEncryption: "AES256" }),
};

for (const [name, make] of Object.entries(cases)) {
  test(`${name} is 501 NotImplemented`, async () => {
    await expectS3Error(assert, client.send(make()), "NotImplemented", 501);
  });
}
