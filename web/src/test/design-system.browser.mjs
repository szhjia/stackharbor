import {chromium, expect} from '@playwright/test';
import {mkdir, writeFile} from 'node:fs/promises';
const out = process.argv[2] || '/tmp/stackharbor-design-system';
const baseURL = process.env.DESIGN_SYSTEM_URL || 'http://127.0.0.1:5174';
await mkdir(out, {recursive:true});
const identity = {session_id:'design-qa', workspace_id:'workspace',protocol_version:1,capabilities:[]};
const metric = {known:true,partial:false,sampled_at:new Date().toISOString(),rss_bytes:104857600,cpu_percent:2.5,uptime_millis:12345};
const node = {id:'backend/api',name:'API service',project_id:'backend',kind:'service',state:'running',reason:'',ownership:'session',allowed_actions:['restart','stop'],resource_refs:['process:123:456'],depends_on:['db'],metric,ports:[{port:5102,status:'owned',reason:'',resource_refs:[]}]};
const session = {identity,root:'/work/design-fixture',pid:123,terminal:'Terminal',tty:'/dev/ttys001',available:true,stale:false,started_at:new Date().toISOString(),last_seen:new Date().toISOString(),snapshot:{identity,revision:1,observed_at:new Date().toISOString(),nodes:[node,{...node,id:'build',name:'Build task',kind:'task',state:'succeeded'}],processes:[],containers:[],diagnostics:[]}};
const resource = {id:'process:123:456',kind:'process',pid:123,identity_known:true,metric,references:[{session_id:identity.session_id,workspace_id:'workspace',node_ids:[node.id],nodes:[{id:node.id,role:'service',ownership:'session',state:'running'}]}]};
const total = {count:1,known_memory_count:1,known_cpu_count:1,memory_bytes:metric.rss_bytes,cpu_percent:2.5,partial:false,sampled_at:metric.sampled_at};
const browser = await chromium.launch({headless:true});
const page = await browser.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message));
await page.route('**/api/v1/**',async route=>{
 const path=new URL(route.request().url()).pathname;
 if(route.request().method() !== 'GET' && !path.endsWith('/auth/session') && !path.endsWith('/plans')) throw new Error(`Unexpected mutation: ${path}`);
 if(path.endsWith('/plans')) {
   const request = route.request().postDataJSON();
   return route.fulfill({json:{data:{id:'plan-qa',session_id:'design-qa',action:request.action,targets:request.targets,affected:request.targets,warnings:[],fingerprint:'qa',expires_at:'2099-01-01T00:00:00Z'}}});
 }

 if(path.endsWith('/events')) return route.fulfill({contentType:'text/event-stream',body:'event: ready\ndata: {}\n\n'});
 let body=path.includes('/auth/')?{csrf:'fixture',expires_at:'2099'}:path.endsWith('/inventory')?{sessions:[{...session,last_seen:new Date().toISOString(),snapshot:{...session.snapshot,observed_at:new Date().toISOString()}}],resources:[resource],totals:{processes:total,containers:{...total,count:0,memory_bytes:0}},partial:false,collected_at:new Date().toISOString(),event_cursor:'event:1'}:path.includes('/logs')?{session_id:'design-qa',target:'',entries:[{sequence:1,time:new Date().toISOString(),project_id:'backend',service_id:node.id,stream:'stdout',text:'Listening on port 5102'}],gap:false,dropped:0,reset:false,next_cursor:1,cursor:'1'}:path.endsWith('/operations')?[{id:'op-qa',session_id:'design-qa',action:'restart',state:'succeeded',created_at:new Date().toISOString(),results:[{target:node.id,state:'succeeded'}]}]:{};
 await route.fulfill({json:{data:body}});
});
const results=[];
for(const width of [1440,900,760,640,390,320]) {
 await page.setViewportSize({width,height:900});
 for(const path of ['/', '/workspaces','/resources','/operations','/workspaces/design-qa','/workspaces/design-qa/tasks','/workspaces/design-qa/resources','/workspaces/design-qa/logs']) {
 await page.goto(baseURL+path); await page.getByRole('button',{name:'Refresh',exact:true}).waitFor();
 await page.locator('.app-content').getByText(/Waiting for the first/).waitFor({state:'hidden'});
 const geometry=await page.evaluate(()=>({viewport:innerWidth,scroll:document.documentElement.scrollWidth,content:document.querySelector('.app-content').getBoundingClientRect().width}));
 if(geometry.scroll>width) throw new Error(`Overflow ${width} ${path}: ${JSON.stringify(geometry)}`);
 if(width===1440||width===390) await page.screenshot({path:`${out}/${width}-${path.replaceAll('/','_')||'overview'}.png`,fullPage:true,animations:"disabled"});
 results.push({width,path,...geometry});
 }
}
// Verify that changing a single token updates both utility and application CSS.
await page.setViewportSize({width:1440,height:900});
await page.goto(baseURL);
await page.locator('.overview-summary').first().waitFor();
await page.addStyleTag({content:"*, *::before, *::after { transition: none !important; animation: none !important; }"});
const tokenChecks = await page.evaluate(() => {
  const root = document.documentElement;
  const css = (selector, property) => { const element=document.querySelector(selector); if(!element) throw new Error(`Missing token check element: ${selector}`); return getComputedStyle(element)[property]; };
  root.style.setProperty('--spacing', '5px');
  const spacing = {panel: css('.overview-summary','paddingLeft'), button: css('.sidebar-toggle','height')};
  root.style.removeProperty('--spacing');
  root.style.setProperty('--type-caption', '16px');
  const typography = {caption: css('.caption','fontSize'), button: css('.connection [data-slot="button"]','fontSize')};
  root.style.removeProperty('--type-caption');
  root.style.setProperty('--primary', 'rgb(128, 40, 150)');
  const color = {link: css('.overview-summary a','color'), marker: css('.observation-summary > div','borderLeftColor')};
  root.style.removeProperty('--primary');
  root.style.setProperty('--radius-panel', '14px');
  const radius = css('.panel','borderTopLeftRadius');
  root.style.removeProperty('--radius-panel');
  root.classList.add('dark');
  const dark = {card: css('.panel','backgroundColor'), page: css('body','backgroundColor')};
  root.classList.remove('dark');
  return {spacing, typography, color, radius, dark};
});
if(tokenChecks.spacing.panel !== '30px' || tokenChecks.spacing.button !== '40px') throw new Error(`Spacing token did not propagate: ${JSON.stringify(tokenChecks)}`);
if(Object.values(tokenChecks.typography).some(value=>value!=='16px')) throw new Error('Typography token did not propagate');
if(Object.values(tokenChecks.color).some(value=>value!=='rgb(128, 40, 150)')) throw new Error('Color token did not propagate');
if(tokenChecks.radius !== '14px' || tokenChecks.dark.card === tokenChecks.dark.page) throw new Error('Surface tokens did not propagate');
// Exercise entity hover cards, the node menu and review dialog at both sizes.
for(const width of [1440,390]) {
  await page.setViewportSize({width,height:900});
  await page.goto(baseURL+'/workspaces');
  const sessionTrigger = page.getByRole('button',{name:'Session details: design-qa'});
  await sessionTrigger.hover();
  await page.locator('.info-popover-content').waitFor();
  const popover = await page.locator('.info-popover-content').boundingBox();
  if(popover.x < -1 || popover.x + popover.width > width + 1) throw new Error('Popover overflows viewport');
  await page.screenshot({path:`${out}/${width}-popover.png`,fullPage:true,animations:"disabled"});
  await sessionTrigger.blur(); await page.mouse.move(0,0);
  await page.getByRole('link',{name:'/work/design-fixture',exact:true}).click();
  await page.getByRole('button',{name:'Actions for backend/api'}).click();
  await page.getByRole('menuitem',{name:'Restart',exact:true}).click();
  await page.getByRole('dialog').waitFor();
  const dialog = await page.getByRole('dialog').boundingBox();
  if(dialog.x < -1 || dialog.x + dialog.width > width + 1) throw new Error('Dialog overflows viewport');
  await page.screenshot({path:`${out}/${width}-dialog.png`,fullPage:true,animations:"disabled"});
  await page.keyboard.press('Escape');
  await page.getByRole('dialog').waitFor({state:'hidden'});
  await expect(page.getByRole('button',{name:'Actions for backend/api'})).toBeFocused();
  await page.getByRole('button',{name:'Collapse sidebar'}).click();
  await page.getByRole('button',{name:'Expand sidebar'}).waitFor();
  await page.getByRole('button',{name:'Expand sidebar'}).click();
}
if(errors.length) throw new Error(`Browser errors: ${errors.join('; ')}`);
await writeFile(out+'/result.json',JSON.stringify({errors,results,tokenChecks,interactions:['hover card','action menu','review dialog','focus return','sidebar toggle']},null,2));
console.log(JSON.stringify({errors,pages:results.length,tokenChecks,out})); await browser.close();
