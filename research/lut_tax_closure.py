"""Monotone max-weight closure LUT, compressed to observed cells.

Unobserved cells have zero weight. Transitive dominance between observed
cells preserves the original six-axis min-cut problem exactly; expand the
minimal selected closure afterward. Uses numba, no shared-script imports.
"""
import numpy as np
from numba import njit


def implies(source, destinations):
    """Original CW order: DTE down, IV up, straddle down, BuildIV up;
    gap/ADJ extremes imply the middle bucket.
    """
    return ((destinations[:,0]<=source[0]) & (destinations[:,1]>=source[1])
            & (destinations[:,2]<=source[2]) & (destinations[:,3]>=source[3])
            & ((destinations[:,4]==source[4]) | (destinations[:,4]==1))
            & ((destinations[:,5]==source[5]) | (destinations[:,5]==1)))


@njit(cache=True)
def _reachable(weights, sources, destinations):
    n=len(weights); source=n; sink=n+1
    edges=2*(len(sources)+n)
    heads=np.full(n+2,-1,np.int32); to=np.empty(edges,np.int32); nxt=np.empty(edges,np.int32)
    capacities=np.zeros(edges,np.int64); pos=0
    inf=np.sum(np.abs(weights))+1
    for i in range(n+len(sources)):
        if i<n:
            if weights[i]>0:
                a,b,c=source,i,weights[i]
            else:
                a,b,c=i,sink,-weights[i]
        else:
            a,b,c=sources[i-n],destinations[i-n],inf
        to[pos]=b; nxt[pos]=heads[a]; heads[a]=pos; capacities[pos]=c; pos+=1
        to[pos]=a; nxt[pos]=heads[b]; heads[b]=pos; pos+=1
    queue=np.empty(n+2,np.int32)
    while True:
        levels=np.full(n+2,-1,np.int32); levels[source]=0; queue[0]=source; end=1; start=0
        while start<end:
            v=queue[start]; start+=1; e=heads[v]
            while e!=-1:
                u=to[e]
                if capacities[e]>0 and levels[u]<0:
                    levels[u]=levels[v]+1; queue[end]=u; end+=1
                e=nxt[e]
        if levels[sink]<0:
            return levels[:n]>=0
        ptr=heads.copy()
        # Iterative blocking-flow DFS avoids recursive JIT/cache linkage and
        # unbounded Python/C stack depth on larger residual graphs.
        vertices=np.empty(n+2,np.int32); path=np.empty(n+2,np.int32)
        bottle=np.empty(n+2,np.int64)
        vertices[0]=source; bottle[0]=inf; depth=0
        while depth>=0:
            v=vertices[depth]
            if v==sink:
                amount=bottle[depth]
                for k in range(depth):
                    e=path[k]; capacities[e]-=amount; capacities[e^1]+=amount
                depth=0; bottle[0]=inf
                continue
            e=ptr[v]
            while e!=-1:
                if capacities[e]>0 and levels[to[e]]==levels[v]+1:
                    break
                ptr[v]=nxt[e]; e=ptr[v]
            if e==-1:
                levels[v]=-1; depth-=1
                if depth>=0:
                    parent=vertices[depth]; ptr[parent]=nxt[path[depth]]
            else:
                path[depth]=e; vertices[depth+1]=to[e]
                bottle[depth+1]=min(bottle[depth],capacities[e]); depth+=1


def solve(profile, tax, dims):
    """profile maps raw coordinate -> (total post-cost PnL, weighted count)."""
    result=np.zeros(dims,bool)
    if not profile:
        return result
    coords=np.array(list(profile),dtype=np.int32)
    weights=np.array([round((profile[tuple(c)][0]-profile[tuple(c)][1]*tax)*100) for c in coords],dtype=np.int64)
    # Original MIN_TRADES_TO_ANCHOR=1: a fractional event-only cell cannot
    # anchor a positive closure unless its accumulated evidence reaches1.
    counts=np.array([profile[tuple(c)][1] for c in coords])
    weights=np.where((weights>0)&(counts<1.),0,weights)
    # Original solver gives a zero-evidence tie a sink edge of capacity1.
    weights=np.where(weights==0,-1,weights)
    sources=[]; destinations=[]
    for i,coord in enumerate(coords):
        j=np.flatnonzero(implies(coord,coords)); j=j[j!=i]
        sources.extend([i]*len(j)); destinations.extend(j)
    selected=_reachable(weights,np.array(sources,np.int32),np.array(destinations,np.int32))
    grid=np.indices(dims).reshape(len(dims),-1).T
    flat=result.reshape(-1)
    for c in coords[selected]:
        flat |= implies(c,grid)
    return result
