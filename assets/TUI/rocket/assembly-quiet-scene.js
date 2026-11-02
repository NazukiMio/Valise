const meta={name:'头罩合装',note:'六支小型机械臂在箭体上下灵活焊接，头罩下降后仅顶部两支移向接缝，其余继续原处焊接，随后依次停工收回，不规则焊接火星沿弧线落向地面，完成后收臂。',cols:120,rows:34,cell:2,fps:30,ground:'#080e24',palette:['#263751','#61738a','#b8d3db','#eee5d5','#e78165','#f4bd59','#66b6aa','#ef625c']};
function make(){const E=(a,b,t)=>{let q=Math.max(0,Math.min(1,(t-a)/(b-a)));return q*q*q*(q*(q*6-15)+10);};const blend=(a,b,q)=>a+(b-a)*q;
return(t,{color})=>{const pix=new Int8Array(240*136).fill(-1);const point=(x,y,k)=>{x=Math.round(x);y=Math.round(y);if(x>=0&&x<240&&y>=0&&y<136)pix[y*240+x]=k;};const line=(x,y,a,b,k)=>{let n=Math.ceil(Math.max(Math.abs(a-x),Math.abs(b-y)));for(let i=0;i<=n;i++)point(blend(x,a,i/(n||1)),blend(y,b,i/(n||1)),k);};const box=(x,y,w,h,k)=>{for(let j=0;j<h;j++)line(x,y+j,x+w,y+j,k);};const disk=(x,y,r,k)=>{for(let j=-r;j<=r;j++)line(x-Math.sqrt(r*r-j*j),y+j,x+Math.sqrt(r*r-j*j),y+j,k);};
for(let x=10;x<240;x+=44){line(x,18,x,120,0);line(x,18,x+22,4,0);line(x+22,4,x+44,18,0);}line(0,18,239,18,1);line(0,23,239,23,0);line(0,121,239,121,1);for(let x=23;x<240;x+=65)box(x,28,18,1,2);for(let y=83;y<120;y+=17){line(9,y,44,y,1);box(13,y-9,10,8,0);box(29,y-7,12,6,0);}box(102,118,38,3,1);box(199,98,19,21,0);
const engX=121;box(engX-7,111,14,4,1);line(engX-5,115,engX-8,118,2);line(engX+5,115,engX+8,118,2);line(engX-8,118,engX+8,118,2);
// Rounded orange-and-ivory checkerboard hull, tapered waist and swept fins.
const by=59;
for(let y=59;y<111;y++){const w=y<83?13-((y-70)/40)**2:13-(y-83)*.19;for(let x=Math.ceil(121-w);x<=121+w;x++){let k=(Math.floor((y-59)/11)+Math.floor((x-108)/12))%2?4:3;point(x,y,k);}}
disk(117,76,7,4);disk(117,76,5,2);disk(117,76,4,0);line(114,73,116,72,3);
for(let side of [-1,1])for(let j=0;j<25;j++){let inner=11+j*.19,outer=13+10*Math.sin(j/25*Math.PI*.65);line(121+side*inner,88+j,121+side*outer,88+j,4);}
const capY=blend(-32,36,E(5,17,t));for(let j=0;j<23;j++){let w=13*Math.sqrt(Math.max(0,1-((j-23)/23)**2));for(let x=Math.ceil(121-w);x<=121+w;x++)point(x,capY+j,j<10?4:(Math.floor((j-10)/11)+Math.floor((x-108)/12))%2?4:3);}
// Slim curved side conduit.
line(135,78,137,79,2);line(137,79,137,89,2);line(137,89,134,94,2);
// Continuous tool paths. All adjacent keyframes share exactly the same pose.
function path(keys){let p=keys[0];for(let i=1;i<keys.length;i++){let n=keys[i];if(t<=n[0]){let q=E(p[0],n[0],t);return [blend(p[1],n[1],q),blend(p[2],n[2],q)];}p=n;}return p.slice(1);}
function arm(base,keys,side){let [x,y]=path(keys);const ex=base+side*19+(x-base)*.15,ey=83+(y-90)*.25;box(base-7,117,14,4,1);line(base,117,ex,ey,5);line(base+1,117,ex+1,ey,5);disk(ex,ey,3,2);line(ex,ey,x,y,5);line(ex+1,ey,x+1,y,5);disk(x,y,2,1);line(x,y,x-3,y+4,2);line(x,y,x+3,y+4,2);}
// Six compact articulated tools, independently paced along the body.
const tools=[];
for(let i=0;i<6;i++){
const side=i<3?-1:1,n=i%3,baseX=121+side*39,baseY=72+n*20;
const stop=25+[0,3,6,1.5,4.5,7.5][i],gather=n===0?E(17,20,t):0,withdraw=E(stop,stop+3,t);
const workingY=75+n*14+7*Math.sin(t*(.47+n*.11)+i*2.1)+3*Math.sin(t*.83+i);
const seamY=59+1.8*Math.sin(t*.83+i*1.7);
let y=blend(workingY,seamY,gather),x=121+side*(13+1.1*Math.sin(t*.7+i));
x=blend(x,baseX-side*4,withdraw);y=blend(y,baseY-4,withdraw);
const ex=baseX-side*(8+2*Math.sin(t*.35+i)),ey=blend(baseY-9,y+5,.36);
box(baseX-3,baseY-2,6,4,1);line(baseX,baseY,ex,ey,1);disk(ex,ey,2,2);line(ex,ey,x,y,2);disk(x,y,1,5);line(x,y,x-side*2,y+1,1);
tools.push([x,y]);
}
const capTop=blend(-32,36,E(5,17,t));const hy=capTop-5-30*E(19,23,t);box(113,18,16,5,1);if(hy>24)line(121,24,121,hy,1);
// Deterministic irregular event schedule; each burst emits ballistic particles.
for(let i=0;i<6;i++)for(let e=0;e<34;e++){
const birth=.2+e*1.04+hash(i*67+e,31)*.92;
const n=i%3,stop=25+[0,3,6,1.5,4.5,7.5][i];if((n===0&&birth>16&&birth<20)||birth>stop)continue;
const age=t-birth;if(age<0||age>2.7)continue;
const gather=n===0?E(17,20,birth):0,side=i<3?-1:1;
const sy=blend(75+n*14+7*Math.sin(birth*(.47+n*.11)+i*2.1)+3*Math.sin(birth*.83+i),59+1.8*Math.sin(birth*.83+i*1.7),gather);
const sx=121+side*(13+1.1*Math.sin(birth*.7+i));
for(let p=0;p<4;p++){
let life=1.3+hash(e*4+p,i+71)*1.4;if(age>life)continue;
const vx=side*(3+hash(e*7+p,i+11)*11),vy=-5-hash(p+e*3,i+9)*10;
const px=sx+vx*age,py=sy+vy*age+16*age*age;
if(py>120)continue;
const k=age<.16?3:age<.8?5:4;
point(px,py,k);if(age>.2&&age<1.7)line(px-vx*.035,py-(vy+32*age)*.035,px,py,k);
}
}
let rows=[];for(let r=0;r<34;r++){let s='';for(let c=0;c<120;c++){let bits=0,k=0;for(let dy=0;dy<4;dy++)for(let dx=0;dx<2;dx++){let v=pix[(r*4+dy)*240+c*2+dx];if(v>=0&&true){bits|=1<<[[0,3],[1,4],[2,5],[6,7]][dy][dx];k=v;}}s+=String.fromCharCode(0x2800+bits);color[r*120+c]=k;}rows.push(s);}return rows.join('\n');};}
