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
const browser = await chromium.launch({ channel: process.env.CI ? undefined : 'chrome',headless:true});
const page = await browser.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message));
async function checkNavigationLabels(width) {
 if(width>640)return;
 const overflow=await page.locator('.console-nav a').evaluateAll(links=>links.flatMap(link=>{
   const label=link.querySelector('span');if(!label)return [];
   const bounds=link.getBoundingClientRect(),text=label.getBoundingClientRect();
   return text.left<bounds.left-1||text.right>bounds.right+1||text.height>parseFloat(getComputedStyle(label).lineHeight)*2+1?[label.textContent]:[];
 }));
 if(overflow.length)throw new Error(`Navigation labels overflow or break across too many lines at ${width}px: ${overflow.join(', ')}`);
}
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
 await checkNavigationLabels(width);
 if(width===1440||width===390) await page.screenshot({path:`${out}/${width}-${path.replaceAll('/','_')||'overview'}.png`,fullPage:true,animations:"disabled"});
 results.push({width,path,...geometry});
 }
}
// Memory ranking uses the remaining viewport height with a 400px minimum.
const memorySizes=[];
for(const height of [700,1100]) {
 await page.setViewportSize({width:1440,height});
 await page.goto(baseURL);
 await page.locator('.workbench-memory-card').waitFor();
 const size=await page.evaluate(()=>{
  const card=document.querySelector('.workbench-memory-card');
  const content=document.querySelector('.workbench-memory-content');
  return {height:card.getBoundingClientRect().height,minHeight:getComputedStyle(card).minBlockSize,overflowY:getComputedStyle(content).overflowY};
 });
 if(size.height<400||size.minHeight!=='400px'||size.overflowY!=='auto') throw new Error(`Memory card sizing failed at ${height}: ${JSON.stringify(size)}`);
 memorySizes.push({height,...size});
}
if(memorySizes[1].height<=memorySizes[0].height) throw new Error(`Memory card did not adapt to viewport height: ${JSON.stringify(memorySizes)}`);
// Inventory notices use one bottom-right toast rather than an inline page banner.
await page.route('**/api/v1/inventory*',route=>route.fulfill({json:{data:{sessions:[session],resources:[resource],totals:{processes:total,containers:total},partial:true,collected_at:new Date().toISOString(),event_cursor:'event:1'}}}));
await page.goto(baseURL);
await page.locator('[data-sonner-toast]').filter({hasText:'Partial observation'}).waitFor();
const toaster=page.locator('[data-sonner-toaster]');
if(await toaster.getAttribute('data-x-position')!=='right'||await toaster.getAttribute('data-y-position')!=='bottom') throw new Error('Notifications are not bottom-right');
if(await page.locator('.console-notice').count()) throw new Error('Inventory notice is still inline');
await page.screenshot({path:`${out}/1440-toast.png`,fullPage:true,animations:"disabled"});
await page.unroute('**/api/v1/inventory*');
// Verify that changing a single token updates both utility and application CSS.
await page.setViewportSize({width:1440,height:900});
await page.goto(baseURL);
await page.locator('.workbench [data-slot="card-content"]').first().waitFor();
await page.addStyleTag({content:"*, *::before, *::after { transition: none !important; animation: none !important; }"});
const baseType = await page.evaluate(() => {
  const size = selector => getComputedStyle(document.querySelector(selector)).fontSize;
  return {body: size('body'), nav: size('.console-nav [data-slot="navigation-menu-link"]'), caption: size('.caption'), heading: size('.workbench-secondary h2'), stat: size('.workbench-stat')};
});
if (JSON.stringify(baseType) !== JSON.stringify({body:'12px', nav:'12px', caption:'12px', heading:'18px', stat:'24px'})) throw new Error(`Unexpected workbench type scale: ${JSON.stringify(baseType)}`);
await page.goto(baseURL+'/resources');
await page.locator('.data-list [data-slot="table-cell"]').first().waitFor();
const tableType = await page.evaluate(() => {
  const size = selector => getComputedStyle(document.querySelector(selector)).fontSize;
  return {head: size('.data-list [data-slot="table-head"]'), cell: size('.data-list [data-slot="table-cell"]'), input: size('.filter-field input'), button: size('.connection [data-slot="button"]')};
});
if (JSON.stringify(tableType) !== JSON.stringify({head:'12px', cell:'12px', input:'12px', button:'12px'})) throw new Error(`Unexpected data type scale: ${JSON.stringify(tableType)}`);
await page.goto(baseURL);
await page.locator('.workbench [data-slot="card-content"]').first().waitFor();
await page.addStyleTag({content:"*, *::before, *::after { transition: none !important; animation: none !important; }"});
const tokenChecks = await page.evaluate(() => {
  const root = document.documentElement;
  const css = (selector, property) => { const element=document.querySelector(selector); if(!element) throw new Error(`Missing token check element: ${selector}`); return getComputedStyle(element)[property]; };
  root.style.setProperty('--spacing', '5px');
  const spacing = {panel: css('.workbench [data-slot="card-content"]','paddingLeft'), button: css('.sidebar-toggle','height')};
  root.style.removeProperty('--spacing');
  root.style.setProperty('--type-sm', '16px');
  const typography = {caption: css('.caption','fontSize'), badge: css('.connection [data-slot="badge"]','fontSize')};
  root.style.removeProperty('--type-sm');
  root.style.setProperty('--type-body', '16px');
  typography.button = css('.connection [data-slot="button"]','fontSize');
  root.style.removeProperty('--type-body');
  root.style.setProperty('--primary', 'rgb(128, 40, 150)');
  const color = {link: css('.workbench-link','color'), marker: css('.workbench-rank-bar','backgroundColor')};
  root.style.removeProperty('--primary');
  root.style.setProperty('--radius', '10px');
  const radius = css('.workbench [data-slot=card]','borderTopLeftRadius');
  root.style.removeProperty('--radius');
  root.classList.add('dark');
  const dark = {card: css('.workbench [data-slot=card]','backgroundColor'), page: css('body','backgroundColor')};
  root.classList.remove('dark');
  return {spacing, typography, color, radius, dark};
});
if(tokenChecks.spacing.panel !== '15px' || tokenChecks.spacing.button !== '40px') throw new Error(`Spacing token did not propagate: ${JSON.stringify(tokenChecks)}`);
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
  const expandedToggle = await page.locator('.sidebar-toggle').boundingBox();
  const expandedMain = await page.locator('.main').boundingBox();
  const expandedBreadcrumb = await page.locator('.app-breadcrumb').boundingBox();
  const expandedIcon = await page.locator('.sidebar-toggle svg').getAttribute('class');
  const expandedNavGap = await page.locator('.console-nav [data-slot="navigation-menu-list"]').first().evaluate(element => getComputedStyle(element).gap);
  const readItemGeometry = () => page.locator('.console-nav [data-slot="navigation-menu-link"]').evaluateAll(links => links.map(link => {const rect = link.getBoundingClientRect();const style = getComputedStyle(link);return {x:rect.x,y:rect.y,width:rect.width,height:rect.height,padding:style.padding}}));
  const expandedItems = await readItemGeometry();
  await page.getByRole('button',{name:'Collapse sidebar'}).click();
  await page.getByRole('button',{name:'Expand sidebar'}).waitFor();
  const transitionSamples = [];
  for(const delay of [0,50,120,300]) {
   if(delay) await page.waitForTimeout(delay-(transitionSamples.at(-1)?.delay??0));
   transitionSamples.push({delay,items:await readItemGeometry()});
  }
  for(const sample of transitionSamples) {
   if(JSON.stringify(sample.items) !== JSON.stringify(transitionSamples[0].items)) throw new Error(`Navigation geometry changed during collapse at ${width}px/${sample.delay}ms: ${JSON.stringify(transitionSamples)}`);
  }
  const collapsedToggle = await page.locator('.sidebar-toggle').boundingBox();
  const collapsedMain = await page.locator('.main').boundingBox();
  const collapsedBreadcrumb = await page.locator('.app-breadcrumb').boundingBox();
  const collapsedIcon = await page.locator('.sidebar-toggle svg').getAttribute('class');
  const collapsedNavGap = await page.locator('.console-nav [data-slot="navigation-menu-list"]').first().evaluate(element => getComputedStyle(element).gap);
  const collapsedItems = await readItemGeometry();
  const rowLayout = items => items.map(({y,height,padding}) => ({y,height,padding}));
  if(width > 640 && JSON.stringify(rowLayout(expandedItems)) !== JSON.stringify(rowLayout(collapsedItems))) throw new Error(`Navigation row geometry changed at ${width}px: ${JSON.stringify({expandedItems,collapsedItems})}`);
  const originalToggleX = width === 1440 ? 248 : 16;
  if(expandedToggle.x !== originalToggleX) throw new Error(`Sidebar toggle left its original position: expected x=${originalToggleX}, received ${expandedToggle.x}`);
  const expandedOffsets = {toggle:expandedToggle.x-expandedMain.x,breadcrumb:expandedBreadcrumb.x-expandedToggle.x};
  const collapsedOffsets = {toggle:collapsedToggle.x-collapsedMain.x,breadcrumb:collapsedBreadcrumb.x-collapsedToggle.x};
  if(JSON.stringify(expandedOffsets) !== JSON.stringify(collapsedOffsets)) throw new Error(`Header-relative geometry changed at ${width}px: ${JSON.stringify({expandedOffsets,collapsedOffsets})}`);
  if(!expandedIcon?.includes('lucide-panel-left-close') || !collapsedIcon?.includes('lucide-panel-left-open') || expandedIcon === collapsedIcon) throw new Error(`Sidebar icons do not reflect state: ${expandedIcon} → ${collapsedIcon}`);
  if(expandedNavGap !== collapsedNavGap) throw new Error(`Navigation gap changed at ${width}px: ${expandedNavGap} → ${collapsedNavGap}`);
  if(await page.locator('.brand small').count()) throw new Error('Brand subtitle should be removed');
  await page.screenshot({path:`${out}/${width}-sidebar-collapsed.png`,fullPage:true,animations:'disabled'});
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
  await checkNavigationLabels(width);
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
// Light sidebar colors must remain readable without changing the log surface.
await page.emulateMedia({colorScheme:'light'});
await page.setViewportSize({width:1440,height:900});
await page.goto(baseURL);
await page.locator('.workbench').waitFor();
await page.addStyleTag({content:"*, *::before, *::after { transition: none !important; animation: none !important; }"});
const sidebarLog = await page.evaluate(() => {
 const css = (selector, property) => getComputedStyle(document.querySelector(selector))[property];
 const channels = value => value.match(/[\d.]+/g).slice(0,3).map(Number);
 const luminance = value => channels(value).map(channel => {
  const linear = channel / 255;
  return linear <= .04045 ? linear / 12.92 : ((linear + .055) / 1.055) ** 2.4;
 }).reduce((sum, channel, index) => sum + channel * [.2126,.7152,.0722][index], 0);
 const contrastRatio = (foreground, background) => {
  const values = [luminance(foreground),luminance(background)].sort((a,b) => b-a);
  return (values[0] + .05) / (values[1] + .05);
 };
 const sidebar = css('.sidebar','backgroundColor');
 const foreground = css('.console-nav a:not([aria-current="page"])','color');
 const activeForeground = css('.console-nav [aria-current="page"]','color');
 const activeBackground = css('.console-nav [aria-current="page"]','backgroundColor');
 return {sidebar,foreground,activeForeground,activeBackground,ratios:{
  foreground:contrastRatio(foreground,sidebar),
  active:contrastRatio(activeForeground,activeBackground),
 }};
});
if(sidebarLog.sidebar !== 'rgb(232, 240, 244)' || sidebarLog.foreground !== 'rgb(36, 59, 74)' || sidebarLog.activeBackground !== 'rgb(201, 221, 229)' || sidebarLog.activeForeground !== 'rgb(23, 63, 76)') throw new Error(`Unexpected light sidebar colors: ${JSON.stringify(sidebarLog)}`);
const expectedRatios = {foreground:10.11,active:8.08};
for(const [name,expected] of Object.entries(expectedRatios)) {
 if(Math.abs(sidebarLog.ratios[name]-expected) > .02 || sidebarLog.ratios[name] < 4.5) throw new Error(`Sidebar contrast failed for ${name}: ${sidebarLog.ratios[name]}`);
}
await page.screenshot({path:`${out}/1440-sidebar-light.png`,fullPage:true,animations:'disabled'});
await page.setViewportSize({width:390,height:900});
await page.screenshot({path:`${out}/390-sidebar-light.png`,fullPage:true,animations:'disabled'});
await page.setViewportSize({width:1440,height:900});
await page.goto(baseURL+'/workspaces/design-qa/logs');
await page.locator('.log-lines').waitFor();
sidebarLog.log = await page.evaluate(() => ({
 background:getComputedStyle(document.querySelector('.log-lines')).backgroundColor,
 foreground:getComputedStyle(document.querySelector('.log-lines')).color,
 sidebar:getComputedStyle(document.querySelector('.sidebar')).backgroundColor,
}));
if(sidebarLog.log.background !== 'rgb(34, 60, 80)' || sidebarLog.log.foreground !== 'rgb(237, 244, 248)' || sidebarLog.log.background === sidebarLog.log.sidebar) throw new Error(`Log surface followed the sidebar: ${JSON.stringify(sidebarLog.log)}`);
await page.screenshot({path:`${out}/1440-log-independent.png`,fullPage:true,animations:'disabled'});
await page.setViewportSize({width:390,height:900});
await page.screenshot({path:`${out}/390-log-independent.png`,fullPage:true,animations:'disabled'});
sidebarLog.dark = await page.evaluate(() => {
 const root = document.documentElement;
 root.classList.add('dark');
 const styles = getComputedStyle(root);
 const result = {
  logSurface:styles.getPropertyValue('--log-surface').trim(),
  logForeground:styles.getPropertyValue('--log-foreground').trim(),
  sidebar:styles.getPropertyValue('--sidebar').trim(),
  sidebarForeground:styles.getPropertyValue('--sidebar-foreground').trim(),
 };
 root.classList.remove('dark');
 return result;
});
const darkSurfaces = ['oklch(0.205 0 0)','oklch(20.5% 0 0)'];
const darkForegrounds = ['oklch(0.985 0 0)','oklch(98.5% 0 0)'];
if(!darkSurfaces.includes(sidebarLog.dark.logSurface) || !darkForegrounds.includes(sidebarLog.dark.logForeground) || !darkSurfaces.includes(sidebarLog.dark.sidebar) || !darkForegrounds.includes(sidebarLog.dark.sidebarForeground)) throw new Error(`Dark surface tokens regressed: ${JSON.stringify(sidebarLog.dark)}`);
// Local clocks preserve the five-second stale boundary without a shell-wide timer.
const fixedObservedAt = new Date().toISOString();
const fixedInventory = async route => route.fulfill({json:{data:{sessions:[{...session,last_seen:fixedObservedAt,snapshot:{...session.snapshot,observed_at:fixedObservedAt}}],resources:[resource],totals:{processes:total,containers:{...total,count:0,memory_bytes:0}},partial:false,collected_at:fixedObservedAt,event_cursor:'event:1'}}});
await page.route('**/api/v1/inventory*',fixedInventory);
await page.clock.install({time:new Date()});
await page.goto(baseURL+'/workspaces');
await page.locator('.data-list [data-slot="badge"]').filter({hasText:'Live'}).waitFor();
await page.clock.runFor(6000);
await page.locator('.data-list [data-slot="badge"]').filter({hasText:'stale'}).waitFor();
await page.getByRole('link',{name:'/work/design-fixture',exact:true}).click();
const closeSession = page.getByRole('button',{name:'Close session',exact:true});
await closeSession.waitFor();
const clockChecks = {initial:'Live',afterSixSeconds:'stale',closeDisabled:await closeSession.isDisabled(),routes:[]};
if(!clockChecks.closeDisabled) throw new Error('Stale workspace still accepts mutations');
for(const [path,heading] of [['/settings','Settings'],['/resources','Runtime resources'],['/operations','Operations']]) {
 await page.goto(baseURL+path);
 await page.getByRole('heading',{name:heading,exact:true,level:1}).waitFor();
 if(new URL(page.url()).pathname !== path) throw new Error(`Route reset after clock advance: ${path}`);
 clockChecks.routes.push(path);
}
await page.unroute('**/api/v1/inventory*',fixedInventory);
// Operation requests are SSE-driven while live, with a measured disconnected fallback.
const operationPage = await browser.newPage();
operationPage.on('pageerror',error => errors.push(error.message));
await operationPage.addInitScript(() => {
 window.__mockEventSource = undefined;
 window.EventSource = class {
  constructor(url) {
   this.url = url;
   this.listeners = new Map();
   window.__mockEventSource = this;
  }
  addEventListener(name, listener) { this.listeners.set(name,listener); }
  close() { this.closed = true; }
  emit(name, data = {}, lastEventId = '') {
   this.listeners.get(name)?.(new MessageEvent(name,{data:JSON.stringify(data),lastEventId}));
  }
 };
});
const operationRequests = {list:0,exact:0};
let operationState = 'running';
await operationPage.route('**/api/v1/**',async route => {
 const path = new URL(route.request().url()).pathname;
 if(route.request().method() !== 'GET') throw new Error(`Unexpected operation mutation: ${path}`);
 if(path.endsWith('/auth/session')) return route.fulfill({json:{data:{csrf:'fixture',expires_at:'2099'}}});
 if(path.endsWith('/inventory')) return route.fulfill({json:{data:{sessions:[session],resources:[resource],totals:{processes:total,containers:total},partial:false,collected_at:new Date().toISOString(),event_cursor:'event:1'}}});
 if(path.endsWith('/sessions/design-qa/operations')) {
  operationRequests.list++;
  return route.fulfill({json:{data:[{id:'op-live',session_id:'design-qa',action:'restart',state:operationState,created_at:'2026-11-04T00:00:00Z',results:[]}]}});
 }
 if(path.endsWith('/sessions/design-qa/operations/op-live')) {
  operationRequests.exact++;
  return route.fulfill({json:{data:{id:'op-live',session_id:'design-qa',action:'restart',state:operationState,created_at:'2026-11-04T00:00:00Z',results:[]}}});
 }
 return route.fulfill({json:{data:{}}});
});
await operationPage.clock.install({time:new Date()});
await operationPage.goto(baseURL+'/operations?session=design-qa&operation=op-live');
await operationPage.locator('.data-list').waitFor();
await expect.poll(() => operationRequests.list).toBe(1);
await expect.poll(() => operationRequests.exact).toBe(1);
await operationPage.evaluate(() => window.__mockEventSource.onopen?.());
await operationPage.locator('.connection [data-slot="badge"]').filter({hasText:'Live'}).waitFor();
const operationChecks = {initial:{...operationRequests},stages:[]};
await operationPage.clock.runFor(6000);
if(operationRequests.list !== 1 || operationRequests.exact !== 1) throw new Error(`Live operations polled on a timer: ${JSON.stringify(operationRequests)}`);
operationChecks.stages.push({name:'live-six-seconds',...operationRequests});
await operationPage.evaluate(() => window.__mockEventSource.emit('inventory',{},'event:2'));
await expect.poll(() => operationRequests.list).toBe(2);
await expect.poll(() => operationRequests.exact).toBe(2);
operationState = 'succeeded';
await operationPage.evaluate(() => window.__mockEventSource.emit('inventory',{},'event:3'));
await expect.poll(() => operationRequests.list).toBe(3);
await expect.poll(() => operationRequests.exact).toBe(3);
await operationPage.getByText('succeeded',{exact:true}).waitFor();
await operationPage.clock.runFor(6000);
if(operationRequests.exact !== 3) throw new Error(`Terminal exact operation kept polling: ${JSON.stringify(operationRequests)}`);
operationChecks.stages.push({name:'terminal-six-seconds',...operationRequests});
await operationPage.evaluate(() => window.__mockEventSource.onerror?.());
await operationPage.clock.runFor(1000);
await expect.poll(() => operationRequests.list).toBe(4);
if(operationRequests.exact !== 3) throw new Error('Terminal exact query resumed during disconnect recovery');
await operationPage.clock.runFor(5000);
await expect.poll(() => operationRequests.list).toBe(5);
if(operationRequests.exact !== 3) throw new Error('Terminal exact query resumed during disconnected fallback');
operationChecks.stages.push({name:'disconnected-five-second-fallback',...operationRequests});
await operationPage.evaluate(() => {
 window.__mockEventSource.onopen?.();
 window.__mockEventSource.emit('inventory',{},'event:5');
});
await expect.poll(() => operationRequests.list).toBe(6);
if(operationRequests.exact !== 3) throw new Error('Terminal exact query resumed after SSE recovery');
if(await operationPage.getByRole('link',{name:'op-live',exact:true}).count() !== 1) throw new Error('SSE recovery duplicated operation rows');
operationChecks.stages.push({name:'sse-recovery',...operationRequests});
operationChecks.final = {...operationRequests};
await operationPage.close();
// Every shared data list exposes a localized table name and column scopes.
const accessibilityChecks = [];
await page.setViewportSize({width:390,height:900});
for(const [path,name] of [['/workspaces','workspaces'],['/resources','resources'],['/operations','operations'],['/workspaces/design-qa','services'],['/workspaces/design-qa/tasks','tasks']]) {
 await page.goto(baseURL+path);
 const table = page.getByRole('table',{name,exact:true});
 await table.waitFor();
 const scopes = await table.locator('th').evaluateAll(headers => headers.map(header => header.getAttribute('scope')));
 if(scopes.length === 0 || scopes.some(scope => scope !== 'col')) throw new Error(`Missing column scopes on ${path}: ${JSON.stringify(scopes)}`);
 const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
 if(overflow) throw new Error(`Accessible table introduced mobile overflow on ${path}`);
 accessibilityChecks.push({path,name,scopes:scopes.length,overflow});
}
await page.evaluate(() => localStorage.setItem('stackharbor.language','zh-CN'));
await page.goto(baseURL+'/workspaces');
await page.getByRole('table',{name:'工作区',exact:true}).waitFor();
accessibilityChecks.push({path:'/workspaces',name:'工作区',localized:true});
await page.evaluate(() => localStorage.setItem('stackharbor.language','en'));
// Motion uses an isolated real-clock page so stale fixtures cannot leak into these checks.
let motionChecks;
{
const page = await browser.newPage();
page.on('pageerror',error => errors.push(error.message));
await page.route('**/api/v1/**',async route=>{
 const path=new URL(route.request().url()).pathname;
 if(route.request().method() !== 'GET' && !path.endsWith('/auth/session') && !path.endsWith('/plans')) throw new Error(`Unexpected mutation: ${path}`);
 if(path.endsWith('/plans')) {
  const request = route.request().postDataJSON();
  return route.fulfill({json:{data:{id:'plan-qa',session_id:'design-qa',action:request.action,targets:request.targets,affected:request.targets,warnings:[],fingerprint:'qa',expires_at:'2099-01-01T00:00:00Z'}}});
 }
 if(path.endsWith('/events')) return route.fulfill({contentType:'text/event-stream',body:'event: ready\ndata: {}\n\n'});
 const body=path.includes('/auth/')?{csrf:'fixture',expires_at:'2099'}:path.endsWith('/inventory')?{sessions:[{...session,last_seen:new Date().toISOString(),snapshot:{...session.snapshot,observed_at:new Date().toISOString()}}],resources:[resource],totals:{processes:total,containers:{...total,count:0,memory_bytes:0}},partial:false,collected_at:new Date().toISOString(),event_cursor:'event:1'}:path.includes('/logs')?{session_id:'design-qa',target:'',entries:[],gap:false,dropped:0,reset:false,next_cursor:1,cursor:'1'}:path.endsWith('/operations')?[]:{};
 await route.fulfill({json:{data:body}});
});
// Shared controls use explicit token-backed motion and honor reduced motion.
await page.emulateMedia({reducedMotion:'no-preference',colorScheme:'light'});
await page.setViewportSize({width:1440,height:900});
await page.goto(baseURL);
await page.locator('.workbench').waitFor();
motionChecks = await page.evaluate(() => {
 const read = selector => {
  const style = getComputedStyle(document.querySelector(selector));
  return {property:style.transitionProperty,duration:style.transitionDuration};
 };
 return {button:read('.connection [data-slot="button"]'),navigation:read('.console-nav [data-slot="navigation-menu-link"]')};
});
for(const [name,value] of Object.entries(motionChecks)) {
 if(value.property === 'all' || value.duration !== '0.1s') throw new Error(`Invalid ${name} transition: ${JSON.stringify(value)}`);
}
await page.goto(baseURL+'/workspaces/design-qa');
await page.locator('.detail-tabs [data-slot="tabs-trigger"]').first().waitFor();
motionChecks.tabs = await page.locator('.detail-tabs [data-slot="tabs-trigger"]').first().evaluate(element => ({property:getComputedStyle(element).transitionProperty,duration:getComputedStyle(element).transitionDuration}));
if(motionChecks.tabs.property === 'all' || motionChecks.tabs.duration !== '0.1s') throw new Error(`Invalid tabs transition: ${JSON.stringify(motionChecks.tabs)}`);
await page.goto(baseURL);
await page.locator('.workbench').waitFor();
motionChecks.tokenDuration = await page.evaluate(() => {
 const root = document.documentElement;
 root.style.setProperty('--motion-fast','220ms');
 const duration = getComputedStyle(document.querySelector('.connection [data-slot="button"]')).transitionDuration;
 root.style.removeProperty('--motion-fast');
 return duration;
});
if(motionChecks.tokenDuration !== '0.22s') throw new Error(`Motion token did not propagate: ${motionChecks.tokenDuration}`);
await page.emulateMedia({reducedMotion:'reduce'});
await page.goto(baseURL);
await page.locator('.workbench').waitFor();
motionChecks.reduced = await page.evaluate(() => {
 const read = selector => {
  const style = getComputedStyle(document.querySelector(selector));
  return {transition:style.transitionDuration,animation:style.animationDuration};
 };
 return {button:read('.connection [data-slot="button"]'),console:read('.console')};
});
for(const [name,value] of Object.entries(motionChecks.reduced)) {
 if(value.transition !== '0s' || value.animation !== '0s') throw new Error(`Reduced motion failed for ${name}: ${JSON.stringify(value)}`);
}
await page.goto(baseURL+'/workspaces/design-qa');
await page.getByRole('button',{name:'Refresh',exact:true}).click();
await expect(page.getByRole('button',{name:'Actions for backend/api'})).toBeEnabled();
await page.getByRole('button',{name:'Actions for backend/api'}).click();
await page.getByRole('menuitem',{name:'Restart',exact:true}).click();
await page.getByRole('dialog').waitFor();
await page.keyboard.press('Escape');
await page.getByRole('dialog').waitFor({state:'hidden'});
await expect(page.getByRole('button',{name:'Actions for backend/api'})).toBeFocused();
motionChecks.reducedMotionFocusReturn = true;
await page.emulateMedia({reducedMotion:'no-preference'});
await page.close();
}
if(errors.length) throw new Error(`Browser errors: ${errors.join('; ')}`);
const screenshots = [
 ...results.filter(({width}) => width === 1440 || width === 390).map(({width,path}) => `${out}/${width}-${path.replaceAll('/','_')||'overview'}.png`),
 ...['1440-toast.png','1440-popover.png','390-popover.png','1440-dialog.png','390-dialog.png','1440-settings-zh-dark.png','390-settings-zh-dark.png','320-settings-zh-dark.png','1440-settings-en-light.png','390-settings-en-light.png','320-settings-en-light.png','1440-sidebar-light.png','390-sidebar-light.png','1440-sidebar-collapsed.png','390-sidebar-collapsed.png','1440-log-independent.png','390-log-independent.png'].map(name => `${out}/${name}`),
];
await writeFile(out+'/result.json',JSON.stringify({errors,results,baseType,tableType,tokenChecks,sidebarLog,clockChecks,operationChecks,accessibilityChecks,motionChecks,screenshots,interactions:['hover card','action menu','review dialog','focus return','sidebar toggle','settings footer','system appearance changes','language and theme','text size switching','preference persistence','keyboard theme selection']},null,2));
console.log(JSON.stringify({errors,pages:results.length,baseType,tableType,tokenChecks,sidebarLog,clockChecks,operationChecks,accessibilityChecks,motionChecks,screenshots:screenshots.length,out})); await browser.close();
