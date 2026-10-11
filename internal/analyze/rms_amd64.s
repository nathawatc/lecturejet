#include "textflag.h"

// func hasAVX2() bool
TEXT ·hasAVX2(SB), NOSPLIT, $0-1
	MOVL $0, AX
	CPUID
	CMPL AX, $7
	JL no_avx2
	MOVL $7, AX
	MOVL $0, CX
	CPUID
	BTQ $5, BX
	JNC no_avx2
	MOVB $1, ret+0(FP)
	RET
no_avx2:
	MOVB $0, ret+0(FP)
	RET

// func hasFMA() bool
TEXT ·hasFMA(SB), NOSPLIT, $0-1
	MOVL $0, AX
	CPUID
	CMPL AX, $1
	JL no_fma
	MOVL $1, AX
	MOVL $0, CX
	CPUID
	BTQ $12, CX
	JNC no_fma
	MOVB $1, ret+0(FP)
	RET
no_fma:
	MOVB $0, ret+0(FP)
	RET

// func sumSquaresAVX2(samples []float32) float64
TEXT ·sumSquaresAVX2(SB), NOSPLIT, $0-32
	MOVQ samples_base+0(FP), SI
	MOVQ samples_len+8(FP), CX

	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3

loop32:
	CMPQ CX, $32
	JL loop8
	VMOVUPS (SI), Y4
	VMOVUPS 32(SI), Y5
	VMOVUPS 64(SI), Y6
	VMOVUPS 96(SI), Y7

	VFMADD231PS Y4, Y4, Y0
	VFMADD231PS Y5, Y5, Y1
	VFMADD231PS Y6, Y6, Y2
	VFMADD231PS Y7, Y7, Y3

	ADDQ $128, SI
	SUBQ $32, CX
	JMP loop32

loop8:
	VADDPS Y1, Y0, Y0
	VADDPS Y3, Y2, Y2
	VADDPS Y2, Y0, Y0

loop8_inner:
	CMPQ CX, $8
	JL remainder
	VMOVUPS (SI), Y4
	VFMADD231PS Y4, Y4, Y0
	ADDQ $32, SI
	SUBQ $8, CX
	JMP loop8_inner

remainder:
	// Y0 has 8 floats: a,b,c,d, e,f,g,h
	VEXTRACTF128 $1, Y0, X1
	VADDPS X1, X0, X0
	VSHUFPS $0x4E, X0, X0, X1
	VADDPS X1, X0, X0
	VSHUFPS $0xE5, X0, X0, X1
	VADDSS X1, X0, X0

loop1:
	CMPQ CX, $0
	JE done
	VMOVSS (SI), X1
	VFMADD231SS X1, X1, X0
	ADDQ $4, SI
	DECQ CX
	JMP loop1

done:
	VZEROUPPER
	CVTSS2SD X0, X0
	MOVSD X0, ret+24(FP)
	RET
