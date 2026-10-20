package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

const (
	researchAutoEvery = 2 * time.Hour
	researchAutoCheck = 15 * time.Minute
)

var researchRunning atomic.Bool

func loadResearchReport() (researchReport, bool) {
	raw, err := os.ReadFile(researchReportFile)
	if err != nil {
		return researchReport{}, false
	}
	var rep researchReport
	if json.Unmarshal(raw, &rep) != nil {
		return researchReport{}, false
	}
	return rep, true
}

func researchReportAge() time.Duration {
	st, err := os.Stat(researchReportFile)
	if err != nil {
		return 1<<62 - 1
	}
	return time.Since(st.ModTime())
}

// researchAutoLoop keeps follower snapshots fresh so first-seen times (and
// so followback lags and the tuner) stay accurate without anyone clicking.
func researchAutoLoop(logFn func(string)) {
	time.Sleep(3 * time.Minute)
	for {
		if researchReportAge() >= researchAutoEvery && researchRunning.CompareAndSwap(false, true) {
			err := runResearch(14, true, func(string) {})
			researchRunning.Store(false)
			if err != nil {
				logFn("Research auto-sync: " + err.Error())
			} else if rep, ok := loadResearchReport(); ok {
				for _, line := range researchHeadline(rep, 2) {
					logFn(line)
				}
			}
		}
		time.Sleep(researchAutoCheck)
	}
}

func handleResearchReport(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(researchReportFile)
	if err != nil {
		writeJSON(w, map[string]any{"missing": true, "running": researchRunning.Load()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `{"running":%t,"ageSec":%d,"report":`, researchRunning.Load(), int(researchReportAge().Seconds()))
	_, _ = w.Write(raw)
	_, _ = w.Write([]byte("}"))
}

func handleResearchDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(researchDashboardHTML))
}

const researchDashboardHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>Research</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{--bg:#0f0d14;--card:#1a1622;--line:#2c2538;--txt:#e9e4f2;--dim:#9a90ad;--acc:#b38cff;--good:#4cd38a;--bad:#ff6b81;--warn:#ffc35c;--mid:#7aa7ff}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--txt);font:13px/1.45 system-ui,Segoe UI,sans-serif}
header{display:flex;gap:12px;align-items:center;padding:14px 20px;border-bottom:1px solid var(--line);position:sticky;top:0;background:var(--bg);z-index:5;flex-wrap:wrap}
h1{font-size:17px;margin:0 12px 0 0}button{background:var(--card);color:var(--txt);border:1px solid var(--line);border-radius:7px;padding:6px 12px;cursor:pointer}
button:hover{border-color:var(--acc)}button.on{background:var(--acc);color:#140f1c;border-color:var(--acc)}
#meta{color:var(--dim);margin-left:auto}main{padding:16px 20px;max-width:1500px}
.kpis{display:grid;grid-template-columns:repeat(auto-fill,minmax(170px,1fr));gap:10px;margin-bottom:16px}
.kpi{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:10px 12px}.kpi b{display:block;font-size:20px}.kpi span{color:var(--dim);font-size:12px}
.card{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px 14px;margin-bottom:14px}
.card h2{font-size:14px;margin:0 0 8px}.note{color:var(--dim);font-size:12px;margin:0 0 8px}
table{width:100%;border-collapse:collapse}th,td{padding:5px 8px;border-bottom:1px solid var(--line);text-align:right;white-space:nowrap}
th{color:var(--dim);font-weight:600;cursor:pointer;user-select:none;position:sticky;top:58px;background:var(--card)}th:hover{color:var(--txt)}
td:first-child,th:first-child{text-align:left}td.l,th.l{text-align:left}tr:hover td{background:#221c2d}
.badge{padding:1px 8px;border-radius:9px;font-size:11px;font-weight:600}
.winner,.boost{background:#173b29;color:var(--good)}.dud,.cut{background:#3d1820;color:var(--bad)}.below{background:#3a2c14;color:var(--warn)}
.average,.normal{background:#1b2840;color:var(--mid)}.testing,.explore{background:#2a2433;color:var(--dim)}
.bar{position:relative;width:120px;height:10px;background:#251f30;border-radius:5px;display:inline-block;vertical-align:middle}
.bar i{position:absolute;top:3px;height:4px;background:var(--acc);opacity:.45;border-radius:2px}.bar em{position:absolute;top:0;width:3px;height:10px;background:var(--acc);border-radius:2px}
.bar u{position:absolute;top:-2px;width:1px;height:14px;background:var(--warn)}
.act{display:flex;gap:10px;padding:7px 0;border-bottom:1px solid var(--line)}.act:last-child{border:0}.area{min-width:84px;color:var(--acc);font-weight:600;text-transform:uppercase;font-size:11px;padding-top:2px}
input{background:#120f18;color:var(--txt);border:1px solid var(--line);border-radius:7px;padding:5px 9px;width:220px}
.funnel div{display:flex;align-items:center;gap:10px;margin:3px 0}.funnel label{width:190px;color:var(--dim)}.funnel s{display:block;height:14px;background:var(--acc);border-radius:3px;text-decoration:none}
.grid2{display:grid;grid-template-columns:1fr 1fr;gap:14px}@media(max-width:1000px){.grid2{grid-template-columns:1fr}}
.empty{color:var(--dim);padding:20px;text-align:center}
</style></head><body>
<header><h1>Research</h1><span id="tabs"></span>
<input id="q" placeholder="filter rows…">
<button id="syncBtn" title="Fetch follower lists + DM partners, then rebuild">Sync + rebuild</button>
<button id="rebuildBtn" title="Rebuild from saved data only">Rebuild</button>
<span id="meta"></span></header>
<main id="main"><div class="empty">Loading…</div></main>
<script>
var R=null,TAB=localStorage.getItem("rtab")||"overview",SORT={};
var TABS=[["overview","Overview"],["daily","Daily"],["sources","Seeds & tags"],["accounts","Accounts"],["messaging","Messaging"],["engage","Engage & timing"],["tuner","Tuner"]];
function $(id){return document.getElementById(id)}
function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,function(c){return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c]})}
function pc(x){return (x*100).toFixed(1)+"%"}
function badge(v){if(!v)return "";var c=v=="below average"?"below":v;return '<span class="badge '+esc(c)+'">'+esc(v)+'</span>'}
function bar(s,base){var lo=(s.winLow||0)*100,hi=(s.winHigh||0)*100,w=(s.winRate||0)*100,mx=Math.max(20,base*300);
 function x(v){return Math.min(100,v/mx*100)}
 return '<span class="bar" title="likely '+lo.toFixed(1)+'–'+hi.toFixed(1)+'%"><i style="left:'+x(lo)+'%;width:'+Math.max(1,x(hi)-x(lo))+'%"></i><em style="left:'+x(w)+'%"></em>'+(base>0?'<u style="left:'+x(base*100)+'%"></u>':'')+'</span>'}
function table(id,cols,rows,note){
 var st=SORT[id];if(st){rows=rows.slice().sort(function(a,b){var c=cols[st.i],va=c.v(a),vb=c.v(b);if(typeof va=="string")return st.d*va.localeCompare(vb);return st.d*((va||0)-(vb||0))})}
 var q=$("q").value.toLowerCase();if(q)rows=rows.filter(function(r){return JSON.stringify(r).toLowerCase().indexOf(q)>=0});
 var h='<table data-id="'+id+'"><thead><tr>';cols.forEach(function(c,i){h+='<th class="'+(c.l?"l":"")+'" data-i="'+i+'">'+c.h+(st&&st.i==i?(st.d>0?" ▲":" ▼"):"")+'</th>'});h+='</tr></thead><tbody>';
 if(!rows.length)h+='<tr><td colspan="'+cols.length+'" class="empty">no rows yet</td></tr>';
 rows.forEach(function(r){h+='<tr>';cols.forEach(function(c){h+='<td class="'+(c.l?"l":"")+'">'+(c.f?c.f(r):esc(c.v(r)))+'</td>'});h+='</tr>'});
 return (note?'<p class="note">'+note+'</p>':'')+h+'</tbody></table>'}
function statCols(base){return [
 {h:"",v:function(s){return s.key},l:1},{h:"touches",v:function(s){return s.touches}},{h:"fair-chance",v:function(s){return s.trials}},
 {h:"followbacks",v:function(s){return s.followbacks}},{h:"DMs",v:function(s){return s.dms}},{h:"win %",v:function(s){return s.winRate},f:function(s){return pc(s.winRate)}},
 {h:"likely range",v:function(s){return s.winLow},f:function(s){return bar(s,base)+' '+(s.winLow*100).toFixed(0)+'–'+(s.winHigh*100).toFixed(0)+'%'}},
 {h:"lift",v:function(s){return s.lift||0},f:function(s){return s.lift?s.lift.toFixed(1)+"x":"-"}},
 {h:"lag h",v:function(s){return s.medianLagHours||0},f:function(s){return s.medianLagHours?s.medianLagHours.toFixed(1):"-"}},
 {h:"accs",v:function(s){return s.accounts}},{h:"verdict",v:function(s){return s.verdict||""},f:function(s){return badge(s.verdict)},l:1}]}
function kpi(v,l){return '<div class="kpi"><b>'+v+'</b><span>'+l+'</span></div>'}
function render(){
 $("tabs").innerHTML=TABS.map(function(t){return '<button data-t="'+t[0]+'" class="'+(TAB==t[0]?"on":"")+'">'+t[1]+'</button>'}).join(" ");
 if(!R){$("main").innerHTML='<div class="empty">No report yet — click Sync + rebuild.</div>';return}
 var f=R.fleet,m=R.messaging||{fleet:{}},mf=m.fleet||{},base=f.winRate||0,h="";
 if(TAB=="overview"){
  var early=f.touches?(f.followbacks+f.dms)/f.touches:0;
  h+='<div class="kpis">'+(f.trials>=15?kpi(pc(base),"win rate (likely "+pc(f.winLow)+"–"+pc(f.winHigh)+")"):kpi(pc(early),"won so far ("+(f.followbacks+f.dms)+" of "+f.touches+" touches)"))+kpi(f.trials,"touches 48h+ old")+kpi(f.followbacks,"followbacks")+kpi(f.dms,"DMs from touches")
   +kpi(R.newFollowers,"new followers ("+R.organic+" organic)")+kpi(R.lost,"lost followers")+kpi(mf.chats||0,"chats")+kpi((mf.nudged||0)+" / "+pc(mf.nudgeReplyRate||0),"pin nudges / replied after")+kpi(pc(mf.followRate||0),"chatters who follow")+'</div>';
  h+='<div class="card"><h2>Do this next</h2>'+((R.actions||[]).length?R.actions.map(function(a){return '<div class="act"><span class="area">'+esc(a.area)+'</span><span>'+esc(a.text)+'</span></div>'}).join(""):'<p class="note">Nothing stands out yet.</p>')+'</div>';
  h+='<div class="card"><h2>Top seeds & tags</h2>'+table("ov-src",statCols(base),(R.sources||[]).slice(0,10),"Ranked by the pessimistic end of the win range — volume has to back a high rate. Yellow tick = fleet rate.")+'</div>';
 }else if(TAB=="daily"){
  h+='<div class="card"><h2>Daily trend</h2>'+table("daily",[{h:"day",v:function(d){return d.day},l:1},{h:"touches",v:function(d){return d.touches}},{h:"follows",v:function(d){return d.follows}},
   {h:"fair-chance",v:function(d){return d.trials}},{h:"wins",v:function(d){return d.wins}},{h:"win %",v:function(d){return d.trials?d.wins/d.trials:0},f:function(d){return d.trials?pc(d.wins/d.trials):"-"}},
   {h:"new followers",v:function(d){return d.newFollowers}},{h:"organic",v:function(d){return d.organic}},{h:"lost",v:function(d){return d.lost}},{h:"chats started",v:function(d){return d.dmChats}},
   {h:"replies",v:function(d){return d.replies}},{h:"pin nudges",v:function(d){return d.nudges}}],R.daily||[],"Wins are dated by the day of the touch; new followers by the day they were first seen.")+'</div>';
 }else if(TAB=="sources"){
  h+='<div class="card"><h2>Seed blogs & tags</h2>'+table("src",statCols(base),R.sources||[],"Win = followback or DM within 14 days. Fair-chance = touches 48h+ old, or that already won.")+'</div>';
  h+='<div class="card"><h2>Account × source pairs</h2>'+table("pairs",statCols(base),(R.pairs||[]).filter(function(s){return s.wins>0||s.trials>=15}))+'</div>';
 }else if(TAB=="accounts"){
  h+='<div class="card"><h2>Accounts</h2>'+table("acc",[{h:"account",v:function(a){return a.id},l:1},{h:"blog",v:function(a){return a.blog},l:1},{h:"followers",v:function(a){return a.followers}},
   {h:"+1d",v:function(a){return a.gain1d}},{h:"+7d",v:function(a){return a.gain7d}},{h:"new",v:function(a){return a.newFollowers}},{h:"ours",v:function(a){return a.attributed}},{h:"organic",v:function(a){return a.organic}},{h:"lost",v:function(a){return a.lost}},
   {h:"fair-chance",v:function(a){return a.trials}},{h:"win %",v:function(a){return a.winRate},f:function(a){return pc(a.winRate)}},{h:"likely",v:function(a){return a.winLow},f:function(a){return bar(a,base)}},
   {h:"verdict",v:function(a){return a.verdict||""},f:function(a){return badge(a.verdict)},l:1},{h:"chats",v:function(a){return a.dmChats}},{h:"nudged",v:function(a){return a.dmNudged}},
   {h:"best source",v:function(a){return a.bestSource||""},l:1},{h:"setup",v:function(a){return a.setup||""},l:1}],R.accounts||[])+'</div>';
 }else if(TAB=="messaging"){
  var tot=(m.funnel&&m.funnel[0]&&m.funnel[0].chats)||1;
  h+='<div class="kpis">'+kpi(mf.chats||0,"chats")+kpi(mf.replies||0,"replies sent")+kpi((mf.avgSets||0).toFixed(1),"avg sets per chat")+kpi(mf.medianLatencyMin?Math.round(mf.medianLatencyMin)+"m":"-","median wait for our reply")
   +kpi(mf.nudged||0,"pin nudges")+kpi(pc(mf.nudgeReplyRate||0),"replied after nudge")+kpi(mf.minorsSkipped||0,"under-18 skipped")+kpi(mf.sendFails||0,"send failures")+kpi(m.deadChats||0,"quiet before nudge")+'</div>';
  h+='<div class="grid2"><div class="card"><h2>Chat funnel</h2><div class="funnel">'+(m.funnel||[]).map(function(s){return '<div><label>'+esc(s.step)+'</label><s style="width:'+Math.max(2,s.chats/tot*320)+'px"></s>'+s.chats+' · '+pc(s.pct)+'</div>'}).join("")+'</div></div>';
  h+='<div class="card"><h2>Gone quiet 48h+ — by our last reply</h2>'+table("quiet",[{h:"after",v:function(c){return c.key},l:1},{h:"chats",v:function(c){return c.n}}],m.quietAfter||[])+'</div></div>';
  var dmCols=[{h:"",v:function(s){return s.key},l:1},{h:"chats",v:function(s){return s.chats}},{h:"replies",v:function(s){return s.replies}},{h:"avg sets",v:function(s){return s.avgSets},f:function(s){return s.avgSets.toFixed(1)}},
   {h:"nudged",v:function(s){return s.nudged}},{h:"reach nudge",v:function(s){return s.reachedNudgePct},f:function(s){return pc(s.reachedNudgePct)}},{h:"reply after nudge",v:function(s){return s.nudgeReplyRate},f:function(s){return pc(s.nudgeReplyRate)}},
   {h:"follow",v:function(s){return s.followRate},f:function(s){return pc(s.followRate)}},{h:"median wait",v:function(s){return s.medianLatencyMin},f:function(s){return s.medianLatencyMin?Math.round(s.medianLatencyMin)+"m":"-"}},
   {h:"under-18",v:function(s){return s.minorsSkipped}},{h:"send fails",v:function(s){return s.sendFails}}];
  h+='<div class="card"><h2>By account</h2>'+table("dmacc",dmCols,m.accounts||[])+'</div>';
  h+='<div class="card"><h2>By where the chatter came from</h2>'+table("dmsrc",dmCols,m.sources||[])+'</div>';
  var cnt=[{h:"",v:function(c){return c.key},l:1},{h:"count",v:function(c){return c.n}}];
  h+='<div class="grid2"><div class="card"><h2>Who wrote the replies</h2>'+table("models",cnt,m.models||[])+'</div><div class="card"><h2>Skipped chats</h2>'+table("skips",cnt,m.skips||[])+'</div></div>';
  h+='<div class="grid2"><div class="card"><h2>Stages sent</h2>'+table("stages",cnt,m.stages||[])+'</div><div class="card"><h2>Replies by hour</h2>'+table("dmhours",cnt,m.hours||[])+'</div></div>';
 }else if(TAB=="engage"){
  h+='<div class="card"><h2>Engage style</h2>'+table("types",statCols(base),R.touchTypes||[])+'</div>';
  h+='<div class="card"><h2>Hour of touch</h2>'+table("hours",statCols(base),R.hours||[])+'</div>';
 }else if(TAB=="tuner"){
  h+='<div class="card"><h2>Reach tuner</h2>'+table("tuner",[{h:"source",v:function(s){return s.key},l:1},{h:"fair-chance",v:function(s){return s.trials}},{h:"wins",v:function(s){return s.wins}},
   {h:"win %",v:function(s){return s.rate},f:function(s){return pc(s.rate)}},{h:"likely",v:function(s){return s.low},f:function(s){return bar({winLow:s.low,winHigh:s.high,winRate:s.rate},base)+' '+(s.low*100).toFixed(0)+'–'+(s.high*100).toFixed(0)+'%'}},
   {h:"mode",v:function(s){return s.mode},f:function(s){return badge(s.mode)},l:1}],R.tuner||[],
   "Every reach run orders its seeds and tags by a random draw from each one's likely win rate. boost = proven above fleet (+2 posts); cut = proven dud (1 post); explore = under 15 fair-chance touches. Tuning starts once the fleet has 50 fair-chance touches.")+'</div>';
 }
 $("main").innerHTML=h;
}
function load(){fetch("/api/research/report").then(function(r){return r.json()}).then(function(d){
 R=d.report||null;var age=d.ageSec!=null?Math.round(d.ageSec/60):null;
 $("meta").textContent=(d.running?"syncing… · ":"")+(R?("made "+R.at.slice(0,16).replace("T"," ")+(age!=null?" ("+age+"m ago)":"")+" · tracking since "+(R.trackedSince||"-").slice(0,16).replace("T"," ")):"");
 render()}).catch(function(){$("meta").textContent="GUI not reachable"})}
function run(sync){fetch("/api/research",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({days:14,sync:sync})}).then(function(r){
 $("meta").textContent=r.ok?"working…":"already running";var n=0,t=setInterval(function(){load();if(++n>60)clearInterval(t)},5000)})}
document.addEventListener("click",function(e){var t=e.target;
 if(t.dataset&&t.dataset.t){TAB=t.dataset.t;localStorage.setItem("rtab",TAB);render();return}
 if(t.tagName=="TH"){var id=t.closest("table").dataset.id,i=+t.dataset.i,st=SORT[id];SORT[id]={i:i,d:st&&st.i==i?-st.d:-1};render()}});
$("q").oninput=render;$("syncBtn").onclick=function(){run(true)};$("rebuildBtn").onclick=function(){run(false)};
load();setInterval(load,60000);
</script></body></html>`
