from pathlib import Path
b=Path(__file__).parent
src=(b/'rocket-preview.html').read_text();kit=src.split('<script>')[1].split('const meta=')[0]
piece=r'''
const meta={name:'总装车间',note:'机械臂依次安装发动机、箭体、尾翼、头罩与侧面模块，随后焊接并扫描检查。',cols:120,rows:34,cell:2,fps:18,ground:'#080e24',palette:['#24334b','#536480','#a9c5cf','#ede7d3','#dc7259','#f4bb54','#5aa59f','#889aad']};
function make(){return(t,{color})=>{const W=240,H=136,g=new Uint8Array(W*H),ink=new Uint8Array(W*H);const dot=(x,y,k)=>{x=Math.round(x);y=Math.round(y);if(x>=0&&x<W&&y>=0&&y<H){g[y*W+x]=1;ink[y*W+x]=k;}};const line=(x,y,a,b,k)=>{const n=Math.max(Math.abs(a-x),Math.abs(b-y),1);for(let i=0;i<=n;i++)dot(x+(a-x)*i/n,y+(b-y)*i/n,k);};const rect=(x,y,w,h,k,fill=false)=>{for(let j=0;j<h;j++)for(let i=0;i<w;i++)if(fill||i===0||j===0||i===w-1||j===h-1)dot(x+i,y+j,k);};const ellipse=(x,y,rx,ry,k,fill=true)=>{for(let j=-ry;j<=ry;j++)for(let i=-rx;i<=rx;i++){const d=i*i/(rx*rx)+j*j/(ry*ry);if(d<=1&&(fill||d>.65))dot(x+i,y+j,k);}};const clamp=v=>Math.max(0,Math.min(1,v)),ease=v=>{v=clamp(v);return v*v*(3-2*v);};
// Warehouse structure: trusses, shelves, inspection platform.
for(let x=12;x<240;x+=43){line(x,15,x,119,0);line(x,15,x+21,3,0);line(x+21,3,x+43,15,0);}line(4,15,235,15,1);line(4,20,235,20,0);line(0,120,239,120,1);
for(let x=20;x<230;x+=65){rect(x,24,19,2,t> x/90?3:0,true);}
for(let y=80;y<=112;y+=16){line(9,y,52,y,1);rect(13,y-10,12,10,0);rect(32,y-7,16,7,1);}rect(205,86,24,32,0);for(let y=92;y<117;y+=8)line(209,y,225,y,1);
line(155,83,180,83,1);line(160,83,160,119,1);line(179,83,179,119,1);for(let y=89;y<119;y+=6)line(160,y,179,y,0);
ellipse(215,44,9,9,1,false);for(let a=0;a<3;a++){let q=t*.8+a*2.094;line(215,44,215+7*Math.cos(q),44+7*Math.sin(q),1);}
rect(102,116,38,4,2,true);
// Main fuselage descends, then the nose cap follows on the same hoist.
const bodyY=26+35*ease((t-7)/6),cx=121;const capY=-30+70*ease((t-21)/5),wings=ease((t-15)/5);
// Engine arrives first on a lower manipulator.
const engineX=75+46*ease((t-3)/4);if(t>2){rect(engineX-7,112,15,3,1,true);line(engineX-5,115,engineX-8,119,2);line(engineX+5,115,engineX+8,119,2);line(engineX-8,119,engineX+8,119,2);}
if(t>7){
for(let y=bodyY;y<bodyY+53;y++)for(let x=111;x<=131;x++){let k=(Math.floor((y-bodyY)/12)+Math.floor((x-111)/10))%2?4:3;dot(x,y,k);}ellipse(cx,bodyY+21,6,6,4);ellipse(cx,bodyY+21,4,4,0);dot(cx-2,bodyY+19,3);rect(115,bodyY+53,13,3,1,true);
}
if(t>=21){for(let y=0;y<21;y++){let w=11*Math.sqrt(Math.max(0,1-((y-21)/21)**2));line(cx-w,capY+y,cx+w,capY+y,y<14?4:3);}}
if(t>14)for(let side of [-1,1]){let wx=cx+side*(28-14*wings);for(let y=0;y<17;y++)line(wx,bodyY+39+y,wx+side*(7-y*.35),bodyY+39+y,4);}
// Hoist brings the body and later the nose; the cap ends exactly above bodyY.
const hook=t<14?bodyY-3:t<21?18:t<27?capY-3:capY-3-(t-27)*6;
rect(cx-8,17,16,7,5,true);line(cx,24,cx,hook,1);line(cx,hook,cx-4,hook+4,2);
// Articulated manipulators. Staggered tasks keep each action readable.
function arm(baseX,baseY,elbowX,elbowY,tipX,tipY,open=false){rect(baseX-7,baseY-3,14,5,1,true);line(baseX,baseY,elbowX,elbowY,5);line(baseX+1,baseY,elbowX+1,elbowY,5);ellipse(elbowX,elbowY,3,3,2);line(elbowX,elbowY,tipX,tipY,5);line(elbowX+1,elbowY,tipX+1,tipY,5);ellipse(tipX,tipY,2,2,1);line(tipX,tipY,tipX-3,tipY+(open?5:2),2);line(tipX,tipY,tipX+3,tipY+(open?5:2),2);}
let lX=76,lY=96,rX=169,rY=96;
if(t>3&&t<7){lX=engineX;lY=109;}
if(t>14&&t<21){lX=93+14*wings;lY=101;rX=149-14*wings;rY=101;}
if(t>27&&t<32){const q=ease((t-27)/4);lX=86+24*q;lY=81;rX=157-24*q;rY=81;rect(lX-2,80,4,11,2,true);rect(rX-2,80,4,11,2,true);}
if(t>=32&&t<36){lX=109;lY=64+(t-32)*11;rX=134;rY=64+(t-32)*11;}
arm(69,118,79,86,lX,lY,t>36);arm(175,118,161,86,rX,rY,t>36);
if(t>=32){rect(108,80,4,11,2,true);rect(132,80,4,11,2,true);}
// A few local welding sparks, followed by a quiet inspection light.
if(t>32&&t<36){for(let i=0;i<8;i++){let a=i*.79+t*5,r=2+4*hash(i,Math.floor(t*9));line(134,rY,134+Math.cos(a)*r,rY+Math.sin(a)*r,5);}}
rect(188,101,10,16,1);ellipse(193,106,2,2,t>40?6:5);if(t>37&&t<40){let yy=114-(t-37)*22;line(110,yy,132,yy,6);}
for(let i=0;i<12;i++){let x=20+hash(i,17)*200,y=30+((hash(i,3)*75+t*.7)%75);if(hash(i,9)>.5)dot(x,y,1);}
let rows=[];for(let r=0;r<34;r++){let row='';for(let c=0;c<120;c++){let bits=0,k=0;for(let dy=0;dy<4;dy++)for(let dx=0;dx<2;dx++){let idx=(r*4+dy)*W+c*2+dx;if(g[idx]&&(t>2||hash(c,r)<t/2)){bits|=1<<[[0,3],[1,4],[2,5],[6,7]][dy][dx];k=ink[idx];}}row+=String.fromCharCode(0x2800+bits);color[r*120+c]=k;}rows.push(row);}return rows.join('\n');};}
'''
piece=piece.replace("rows.join('\\\\n')","rows.join('\\n')")
end='''const piece={meta,default:make};const root=document.getElementById('assembly-host');const el=document.createElement('canvas');el.setAttribute('role','img');el.setAttribute('aria-label',meta.note);root.append(el);mount(el,piece);'''
script=kit+piece+end
fragment='<div id="assembly-host"></div>\n<style>#assembly-host{background:#080e24;border-radius:10px;overflow:hidden;width:100%}#assembly-host canvas{display:block;width:100%}</style>\n<script>\n'+script+'\n</script>'
(b/'assembly-preview.html').write_text(fragment)
(b/'assembly.html').write_text('<!doctype html><html lang="zh"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>总装车间</title><body style="margin:0;background:#080e24;display:grid;min-height:100vh;align-items:center">'+fragment+'</body></html>')
(b/'check-assembly.cjs').write_text(kit+piece+"\nfor(const t of [0,4,9,14,19,23,27,33,39,42]){const rows=make()(t,{color:new Uint8Array(4080)}).split('\\n');if(rows.length!==34||rows.some(r=>r.length!==120))throw Error('Invalid frame '+rows.length);}console.log('Assembly frames validated');")
