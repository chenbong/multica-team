// Format only confirmed backup evidence; do not include object paths or hashes.
export function backupSuccessNotice(result) {
  if (!result || result.ok !== true || result.verified !== true ||
      result.database_dump_readable !== true ||
      !Number.isSafeInteger(result.archive_files_verified) || result.archive_files_verified < 7 ||
      !Number.isSafeInteger(result.bytes) || result.bytes <= 0 ||
      !Number.isInteger(result.attempts) || result.attempts < 1 || result.attempts > 3 ||
      typeof result.completed_at !== "string") {
    throw new Error("Backup result is not fully verified");
  }
  const date = new Date(result.completed_at);
  if (!Number.isFinite(date.getTime())) throw new Error("Invalid backup completion time");
  const time = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).format(date);
  const size = (result.bytes / (1024 ** 3)).toFixed(2);
  return `✅ **Team Mesh 每日备份成功**\n\n完成时间：${time}（北京时间）\n备份大小：${size} GiB\n校验结果：BOS 回读与校验和一致，备份包内文件校验通过，数据库转储可完整读取。`;
}
