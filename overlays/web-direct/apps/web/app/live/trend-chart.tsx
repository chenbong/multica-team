"use client";

const numbers = new Intl.NumberFormat("zh-CN");
const weekdays = new Intl.DateTimeFormat("zh-CN", { weekday: "short", timeZone: "Asia/Shanghai" });

function valid(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

export function chartScale(values: (number | null | undefined)[]) {
  const largest = Math.max(0, ...values.filter(valid));
  if (largest === 0) return { max: 1, ticks: [0, 1] };
  const rough = largest / 4;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const factor = [1, 2, 5, 10].find(step => step * magnitude >= rough) ?? 10;
  const step = Math.max(1, factor * magnitude);
  const max = Math.ceil(largest / step) * step;
  return { max, ticks: Array.from({ length: Math.round(max / step) + 1 }, (_, i) => i * step) };
}

export function TrendChart({ dates, values, completed, title, unit, demo=true, partial=[] }: {
  dates: string[];
  values: (number | null)[];
  completed?: (number | null)[];
  title: string;
  unit: string;
  demo?: boolean;
  partial?: boolean[];
}) {
  const scale = chartScale([...values, ...(completed ?? [])]);
  const label = (value: number | null | undefined) => valid(value) ? `${numbers.format(value)} ${unit}` : "暂无数据";
  const datesWithWeekday = dates.map(date => ({ date: date.slice(5).replace("-", "."), weekday: weekdays.format(new Date(`${date}T12:00:00+08:00`)) }));
  return <>
    <div className="tm-live-chartArea" role="group" aria-label={`${title}趋势图`}>
      <div className="tm-live-yAxis" aria-hidden="true">{scale.ticks.map(tick => <span key={tick} style={{ bottom: `${tick / scale.max * 100}%` }}>{numbers.format(tick)}</span>)}</div>
      <div className="tm-live-chartGrid" aria-hidden="true">{scale.ticks.map(tick => <i key={tick} style={{ bottom: `${tick / scale.max * 100}%` }}/>)}</div>
      <div className="tm-live-chart">
        {datesWithWeekday.map(({ date, weekday }, index) => {
          const value = values[index];
          const finished = completed?.[index];
          const top = Math.max(valid(value) ? value : 0, valid(finished) ? finished : 0) / scale.max * 100;
          const collecting = !!partial[index] && valid(value);
          const description = (completed ? `新建任务 ${label(value)}，完成任务 ${label(finished)}` : `${title} ${label(value)}`)+(collecting?"，采集中，仅含已采集时段":"");
          const id = `live-tooltip-${index}`;
          return <div className="tm-live-chartColumn" key={dates[index]} role="img" tabIndex={0} aria-label={`${date}（${weekday}）${description}`} aria-describedby={id}>
            <div className="tm-live-barTrack">
              {valid(value) ? <span className="tm-live-bar" style={{ height: `${value / scale.max * 100}%` }}><span className="tm-live-barValue">{numbers.format(value)}</span></span> : <span className="tm-live-chartMissing">—</span>}
              {completed && (valid(finished) ? <span className="tm-live-completedBar" style={{ height: `${finished / scale.max * 100}%` }}><span className="tm-live-barValue">{numbers.format(finished)}</span></span> : <span className="tm-live-chartMissing">—</span>)}
            </div>
            <div id={id} role="tooltip" className="tm-live-chartTooltip" style={{ bottom: `calc(${top}% + 12px)` }}><span>{date} · {weekday}</span><strong>{completed ? "新建任务" : title}：{label(value)}</strong>{collecting&&<small>采集中 · 仅含已采集时段</small>}{completed && <strong>完成任务：{label(finished)}</strong>}</div>
            <span className="tm-live-axis" aria-hidden="true">{date}<small>{weekday}</small>{collecting&&<small className="tm-live-collecting">采集中</small>}</span>
          </div>;
        })}
      </div>
    </div>
    <table className="tm-live-srOnly"><caption>{demo?`${title}，演示数据`:title}</caption><thead><tr><th scope="col">日期</th><th scope="col">{completed ? "新建任务" : title}</th>{completed && <th scope="col">完成任务</th>}</tr></thead><tbody>{datesWithWeekday.map(({ date, weekday }, i) => <tr key={dates[i]}><th scope="row">{date}（{weekday}）</th><td>{valid(values[i]) ? numbers.format(values[i]!) : "暂无数据"}</td>{completed && <td>{valid(completed[i]) ? numbers.format(completed[i]!) : "暂无数据"}</td>}</tr>)}</tbody></table>
  </>;
}
