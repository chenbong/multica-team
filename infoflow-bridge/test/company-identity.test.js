import assert from 'node:assert/strict';
import test from 'node:test';
import { verificationRecipient } from '../src/company-identity.js';

test('company email identities remain separate and map to exact InfoFlow recipients', () => {
  const roots = ['baidu.com'];
  for (const [email, user] of [['alice@baidu.com','alice'],['employee@sz.baidu.com','employee_sz'],['alice@hk.baidu.com','alice_hk'],['ALICE@SZ.BAIDU.COM','alice_sz']]) {
    assert.equal(verificationRecipient(email, roots), user);
  }
  for (const email of ['alice@a.b.baidu.com','alice@evilbaidu.com','alice@sz.baidu.com.evil','alice@-sz.baidu.com','alice@sz_.baidu.com','alice@@baidu.com']) assert.equal(verificationRecipient(email, roots),null);
  assert.equal(verificationRecipient('v_employee@baidu.com',roots),'v_employee');
  assert.equal(verificationRecipient('v_employee@sz.baidu.com',roots),'v_employee_sz');
  assert.equal(verificationRecipient('alice@child.example.test',['example.test']),null);
});
