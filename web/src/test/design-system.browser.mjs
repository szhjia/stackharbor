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
 for(const path of ['/', '/workspaces','/resources','/operations','/settings','/workspaces/design-qa','/workspaces/design-qa/tasks','/workspaces/design-qa/resources','/workspaces/design-qa/logs']) {
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
const baseType = await page.evaluate(() => {
  const size = selector => getComputedStyle(document.querySelector(selector)).fontSize;
  return {body: size('body'), nav: size('.console-nav [data-slot="navigation-menu-link"]'), caption: size('.caption'), heading: size('.overview-summary h2'), stat: size('.overview-summary-total strong')};
});
if (JSON.stringify(baseType) !== JSON.stringify({body:'12px', nav:'12px', caption:'12px', heading:'18px', stat:'24px'})) throw new Error(`Unexpected overview type scale: ${JSON.stringify(baseType)}`);
await page.goto(baseURL+'/resources');
await page.locator('.data-list [data-slot="table-cell"]').first().waitFor();
const tableType = await page.evaluate(() => {
  const size = selector => getComputedStyle(document.querySelector(selector)).fontSize;
  return {head: size('.data-list [data-slot="table-head"]'), cell: size('.data-list [data-slot="table-cell"]'), input: size('.filter-field input'), button: size('.connection [data-slot="button"]')};
});
if (JSON.stringify(tableType) !== JSON.stringify({head:'12px', cell:'12px', input:'12px', button:'12px'})) throw new Error(`Unexpected data type scale: ${JSON.stringify(tableType)}`);
await page.goto(baseURL);
await page.locator('.overview-summary').first().waitFor();
await page.addStyleTag({content:"*, *::before, *::after { transition: none !important; animation: none !important; }"});
const tokenChecks = await page.evaluate(() => {
  const root = document.documentElement;
  const css = (selector, property) => { const element=document.querySelector(selector); if(!element) throw new Error(`Missing token check element: ${selector}`); return getComputedStyle(element)[property]; };
  root.style.setProperty('--spacing', '5px');
  const spacing = {panel: css('.overview-summary','paddingLeft'), button: css('.sidebar-toggle','height')};
  root.style.removeProperty('--spacing');
  root.style.setProperty('--type-sm', '16px');
  const typography = {caption: css('.caption','fontSize'), badge: css('.connection [data-slot="badge"]','fontSize')};
  root.style.removeProperty('--type-sm');
  root.style.setProperty('--type-body', '16px');
  typography.button = css('.connection [data-slot="button"]','fontSize');
  root.style.removeProperty('--type-body');
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
if(tokenChecks.spacing.panel !== '25px' || tokenChecks.spacing.button !== '40px') throw new Error(`Spacing token did not propagate: ${JSON.stringify(tokenChecks)}`);
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
async function choosePreference(name, option) {
  await page.getByRole('combobox',{name,exact:true}).click();
  await page.getByRole('option',{name:option,exact:true}).click();
}
// Preferences are presentation-only: exercise navigation, persistence and keyboard controls.
for (const width of [1440,390,320]) {
  await page.setViewportSize({width,height:900});
  await page.goto(baseURL);
  const settingsLink = page.locator('.sidebar-footer').getByRole('link',{name:'Settings',exact:true});
  if (width === 1440) {
    const footer = await page.locator('.sidebar-footer').boundingBox();
    if (footer.y + footer.height < 898) throw new Error('Settings footer is not at the bottom of the sidebar');
    await page.getByRole('button',{name:'Collapse sidebar'}).click();
    await expect(settingsLink).toHaveAttribute('title','Settings');
  }
  await settingsLink.click();
  if (width === 1440) await expect(page.getByRole('combobox',{name:'Appearance',exact:true})).toHaveText('Follow system');
  await expect(page.locator('html')).toHaveAttribute('data-font-size','sm');
  await choosePreference('Text size','Medium (13px)');
  await expect.poll(() => page.locator('body').evaluate(element => getComputedStyle(element).fontSize)).toBe('13px');
  await choosePreference('Text size','Large (14px)');
  await expect.poll(() => page.locator('body').evaluate(element => getComputedStyle(element).fontSize)).toBe('14px');
  await page.goto(baseURL+'/resources');
  await page.locator('.data-list [data-slot="table-cell"]').first().waitFor();
  const largeType = await page.evaluate(() => ({
    cell: getComputedStyle(document.querySelector('.data-list [data-slot="table-cell"]')).fontSize,
    nav: getComputedStyle(document.querySelector('.console-nav [data-slot="navigation-menu-link"]')).fontSize,
    body: getComputedStyle(document.body).fontSize,
    fontSize: document.documentElement.dataset.fontSize,
    bodyToken: getComputedStyle(document.documentElement).getPropertyValue('--type-body').trim(),
    scroll: document.documentElement.scrollWidth,
  }));
  if (largeType.cell !== '14px' || largeType.nav !== '14px' || largeType.scroll > width) throw new Error(`Large text did not reach resources at ${width}px: ${JSON.stringify(largeType)}`);
  await page.goto(baseURL+'/settings');
  await page.reload();
  await expect(page.getByRole('combobox',{name:'Text size',exact:true})).toHaveText('Large (14px)');
  await choosePreference('Text size','Small (12px)');
  await expect.poll(() => page.locator('body').evaluate(element => getComputedStyle(element).fontSize)).toBe('12px');
  await choosePreference('Interface language','中文');
  await choosePreference('外观','深色');
  await expect(page.getByRole('heading',{name:'设置',exact:true,level:1})).toBeVisible();
  await expect(page.locator('html')).toHaveClass('dark');
  await expect(page.locator('html')).toHaveAttribute('lang','zh-CN');
  await page.reload();
  await expect(page.getByRole('combobox',{name:'界面语言',exact:true})).toHaveText('中文');
  await expect(page.getByRole('combobox',{name:'外观',exact:true})).toHaveText('深色');
  await expect(page.getByRole('combobox',{name:'文字大小',exact:true})).toHaveText('小（12px）');
  const geometry = await page.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth,card:getComputedStyle(document.querySelector('.settings-panel')).backgroundColor,page:getComputedStyle(document.body).backgroundColor}));
  if(geometry.scroll > width || geometry.card === geometry.page) throw new Error(`Invalid settings layout: ${JSON.stringify(geometry)}`);
  await page.screenshot({path:`${out}/${width}-settings-zh-dark.png`,fullPage:true,animations:'disabled'});
  await page.getByRole('combobox',{name:'外观',exact:true}).focus();
  await page.keyboard.press('Space');
  await page.getByRole('option',{name:'深色',exact:true}).waitFor();
  await expect(page.getByRole('option',{name:'深色',exact:true})).toBeFocused();
  await page.keyboard.press('Home');
  await expect(page.getByRole('option',{name:'跟随系统',exact:true})).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('option',{name:'浅色',exact:true})).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('html')).not.toHaveClass('dark');
  await choosePreference('界面语言','English');
  await page.emulateMedia({colorScheme:'dark'});
  await choosePreference('Appearance','Follow system');
  await expect(page.locator('html')).toHaveClass('dark');
  await expect.poll(() => page.locator('html').evaluate(element => element.style.colorScheme)).toBe('dark');
  await page.emulateMedia({colorScheme:'light'});
  await expect(page.locator('html')).not.toHaveClass('dark');
  await page.reload();
  await expect(page.getByRole('combobox',{name:'Appearance',exact:true})).toHaveText('Follow system');
  await choosePreference('Appearance','Dark');
  await expect(page.locator('html')).toHaveClass('dark');
  await choosePreference('Appearance','Light');
  await expect(page.locator('html')).not.toHaveClass('dark');
  if(width===1440) await page.getByRole('button',{name:'Expand sidebar'}).click();
  await page.screenshot({path:`${out}/${width}-settings-en-light.png`,fullPage:true,animations:'disabled'});
}
if(errors.length) throw new Error(`Browser errors: ${errors.join('; ')}`);
await writeFile(out+'/result.json',JSON.stringify({errors,results,baseType,tableType,tokenChecks,interactions:['hover card','action menu','review dialog','focus return','sidebar toggle','settings footer','system appearance changes','language and theme','text size switching','preference persistence','keyboard theme selection']},null,2));
console.log(JSON.stringify({errors,pages:results.length,baseType,tableType,tokenChecks,out})); await browser.close();
