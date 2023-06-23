package main

/*
#cgo LDFLAGS:  -lfaiss_c
#include <stdio.h>
#include <stdlib.h>
#include <faiss_c.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

type FaissIndex struct {
	idx       C.faiss_Index
	dimension int
}

func NewFaissIndex(dimension, nprobe int) FaissIndex {
	idx := C.faiss_IndexFlatL2_new(C.int(dimension))
	C.faiss_Index_set_nprobe(idx, C.size_t(nprobe))
	return FaissIndex{idx: idx, dimension: dimension}
}

func (fi *FaissIndex) Add(data [][]float32) {
	n := len(data)
	d := len(data[0])
	xb := make([]float32, n*d)
	for i := 0; i < n; i++ {
		for j := 0; j < d; j++ {
			xb[d*i+j] = data[i][j]
		}
	}

	C.faiss_IndexFlatL2_add(fi.idx, C.size_t(n), (*C.float)(unsafe.Pointer(&xb[0])))
	C.free(unsafe.Pointer(&xb[0]))
}

func (fi *FaissIndex) Search(query [][]float32, k int) [][]int32 {
	n := len(query)
	d := len(query[0])
	xq := make([]float32, n*d)
	for i := 0; i < n; i++ {
		for j := 0; j < d; j++ {
			xq[d*i+j] = query[i][j]
		}
	}

	distances := make([]float32, n*k)
	I := make([]int32, n*k)

	C.faiss_IndexFlatL2_search(fi.idx, C.size_t(n), (*C.float)(unsafe.Pointer(&xq[0])), C.size_t(k), (*C.float)(unsafe.Pointer(&distances[0])), (*C.int32_t)(unsafe.Pointer(&I[0])))

	C.free(unsafe.Pointer(&xq[0]))

	results := make([][]int32, n)
	for i := 0; i < n; i++ {
		results[i] = I[i*k : (i+1)*k]
	}

	return results
}

func (fi *FaissIndex) Clear() {
	C.faiss_Index_reset(fi.idx)
}

func (fi *FaissIndex) Delete() {
	C.faiss_Index_free(fi.idx)
}

func main() {
	// create index
	dimension := 128
	nprobe := 4
	faissIndex := NewFaissIndex(dimension, nprobe)
	defer faissIndex.Delete()

	// insert data
	data := [][]float32{{0.1, 2.5, 3.4, 1.3, 2.4}, {1.1, 3.0, 2.4, 4.3, 4.4}, {8.9, 7.8, 6.2, 5.3, 4.2}, {0.1, 2.5, 3.4, 1.3, 2.4}, {1.1, 3.0, 2.4, 4.3, 4.4}}
	faissIndex.Add(data)

	// search index
	query := [][]float32{{0.2, 2.6, 3.5, 1.4, 2.5}}
	results := faissIndex.Search(query, 3)
	fmt.Println(results)
}
