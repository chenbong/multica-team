import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { groupInfo, IssueGroups } from "./src/issue-groups.js";

const id = "1859897297556589312";
test("group info uses exact IDs, official transport and only verified display metadata", async () => {
  const client = { httpClient: { post: async (path, body, options) => {
    assert.equal(path, "/robot/group/getGroupInfo"); assert.equal(body, `{"groupId":${id}}`);
    assert.deepEqual(options, { disableRetry: true });
    return { errcode: 0, data: { groupId: id, name: " TeamMesh 测试群 ", bulletin: "ignored", masterUserId: "ignored" } };
  } } };
  assert.deepEqual(await groupInfo(client, id), { name: "TeamMesh 测试群" });
  for (const data of [{ groupId: "123", name: "wrong" }, { groupId: Number(id), name: "rounded" }, { groupId: id, name: "字".repeat(100) }, { groupId: id }]) {
    client.httpClient.post = async () => ({ errcode: 0, data });
    await assert.rejects(groupInfo(client, id));
  }
  client.httpClient.post = async () => ({ errcode: 403, data: { groupId: id, name: "not authorized" } });
  await assert.rejects(groupInfo(client, id));
});

test("discovery caches names, coalesces lookups and tolerates missing metadata permission", async t => {
  const dir = mkdtempSync(join(tmpdir(), "infoflow-group-name-"));
  t.after(() => rmSync(dir, { force: true, recursive: true }));
  let now = 0, reads = 0, allowed = true;
  const groups = new IssueGroups({ statePath: join(dir, "state"), entries: async () => [], now: () => now,
    roster: async () => ({ bots: [{ appId: "101", imId: null }], members: ["alice"] }),
    info: async () => { reads++; if (!allowed) throw new Error("403"); return { name: "测试群" }; } });
  const scope = { workspace_id: "ws", agent_id: "agent", robot_id: "bot" }, robot = { robotId: "bot", appId: "101", client: {} };
  const published = [];
  await groups.observeGroup(scope, robot, id, async value => published.push(value));
  await groups.observeGroup(scope, robot, id, async value => published.push(value));
  assert.equal(reads, 1); assert.equal(published[0].name, "测试群");
  now = 60001; allowed = false;
  await groups.observeGroup(scope, robot, id, async value => published.push(value));
  assert.equal(published[1].name, ""); assert.deepEqual(published[1].members, ["alice"]);
  groups.roster = async () => ({ bots: [], members: ["alice"] }); now += 60001;
  await assert.rejects(groups.observeGroup(scope, robot, id, async value => published.push(value)));
  assert.equal(reads, 2); assert.equal(published.length, 2);
});
